package gmtool

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dnf90.local/gateway/internal/servicehttp"
)

type queuedMail struct {
	ID              string `json:"id"`
	InitialRevision uint64 `json:"initialRevision"`
	Revision        uint64 `json:"revision"`
	Status          string `json:"status"`
	Retry           bool   `json:"retry"`
	InFlight        bool   `json:"inFlight"`
}
type queueState struct {
	Hash     string                   `json:"pvfSha256"`
	Jobs     map[string]queuedMail    `json:"jobs"`
	Benefits map[string]queuedBenefit `json:"benefits,omitempty"`
}
type queueTask struct {
	ID          string `json:"id"`
	Revision    uint64 `json:"revision"`
	Pending     int    `json:"pending"`
	Failed      int    `json:"failed"`
	PVFChecksum string `json:"pvfChecksum"`
}

// ExternalQueue persists administrator-confirmed work. Delivery and deduplication
// remain authoritative in the original gateway database, including after restart.
type ExternalQueue struct {
	mu        sync.Mutex
	benefitMu sync.Mutex
	path      string
	state     queueState
	peer      *servicehttp.Client
}

func OpenExternalQueue(path string, peer *servicehttp.Client) (*ExternalQueue, error) {
	if path == "" {
		return nil, errors.New("external GM queue path required")
	}
	q := &ExternalQueue{path: path, peer: peer, state: queueState{Hash: peer.Hash(), Jobs: map[string]queuedMail{}}}
	raw, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if e == nil {
		if len(raw) > 2<<20 || json.Unmarshal(raw, &q.state) != nil || q.state.Jobs == nil || len(q.state.Jobs) > 2000 {
			return nil, errors.New("invalid external GM queue")
		}
		for id, j := range q.state.Jobs {
			if !queueID(id) || j.ID != id || (j.Status != "active" && j.Status != "paused" && j.Status != "completed" && j.Status != "conflict") {
				return nil, errors.New("invalid queued mail identity")
			}
		}
		if len(q.state.Benefits) > 1000 {
			return nil, errors.New("invalid benefit queue")
		}
		for id, j := range q.state.Benefits {
			if !queueID(id) || id != j.ID || !queueID(j.CampaignID) || j.StartedAt.IsZero() || len(j.Cursor) > 256 || len(j.Failures) > 10 || (j.Status != "active" && j.Status != "completed" && j.Status != "failed" && j.Status != "conflict") {
				return nil, errors.New("invalid queued benefit")
			}
		}
		if q.state.Hash != peer.Hash() {
			for id, j := range q.state.Benefits {
				if j.Status == "active" {
					j.Status = "conflict"
					j.Error = "PVF 已变化，请核对后重新补发"
					q.state.Benefits[id] = j
				}
			}
			for id, j := range q.state.Jobs {
				if j.Status == "active" {
					j.Status = "conflict"
					q.state.Jobs[id] = j
				}
			}
			q.state.Hash = peer.Hash()
		}
	}
	if q.state.Benefits == nil {
		q.state.Benefits = map[string]queuedBenefit{}
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	if e = q.save(); e != nil {
		return nil, e
	}
	return q, nil
}
func queueID(id string) bool {
	raw, e := hex.DecodeString(id)
	return e == nil && len(raw) == 16 && strings.ToLower(id) == id
}
func (q *ExternalQueue) save() error {
	raw, e := json.Marshal(q.state)
	if e != nil {
		return e
	}
	if len(raw) > 2<<20 {
		return errors.New("external GM queue full")
	}
	f, e := os.CreateTemp(filepath.Dir(q.path), ".queue-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, q.path); e != nil {
		return e
	}
	if dir, e := os.Open(filepath.Dir(q.path)); e == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}
func (q *ExternalQueue) task(ctx context.Context, id string) (queueTask, json.RawMessage, error) {
	var task queueTask
	res, e := q.peer.Do(ctx, "GET", "/api/v1/tasks/"+id, nil, nil)
	if e != nil {
		return task, nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("X-PurpleMagic-Protocol") != servicehttp.Version || res.Header.Get("X-PVF-SHA256") != q.peer.Hash() {
		return task, nil, errors.New("task gateway unavailable")
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if e != nil {
		return task, nil, e
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, &task) != nil || task.ID != id || (task.PVFChecksum != "" && task.PVFChecksum != q.peer.Hash()) {
		return task, nil, errors.New("task identity or PVF changed")
	}
	return task, envelope.Data, nil
}
func (q *ExternalQueue) Status(id string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.state.Jobs[id].Status
}
func (q *ExternalQueue) Handle(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"), "/")
	if len(parts) != 2 || (parts[1] != "schedule" && parts[1] != "pause") {
		return false
	}
	if !queueID(parts[0]) || r.Method != "POST" {
		v1Fail(w, r, 400, "invalid_task", "任务请求无效", false)
		return true
	}
	var in struct {
		Revision uint64 `json:"revision"`
		Confirm  bool   `json:"confirm"`
		Retry    bool   `json:"retry"`
	}
	if !v1Decode(w, r, &in) {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	task, raw, e := q.task(r.Context(), parts[0])
	if e != nil {
		v1Fail(w, r, 503, "gateway_unavailable", "读取任务失败，请重试", true)
		return true
	}
	old, exists := q.state.Jobs[task.ID]
	next := old
	if parts[1] == "pause" {
		next.Status = "paused"
		next.ID = task.ID
		next.Revision = task.Revision
	} else {
		if !in.Confirm {
			v1Fail(w, r, 422, "confirmation_required", "请核对名单并确认发放", false)
			return true
		}
		if exists && old.Status == "active" && in.Revision == old.InitialRevision {
			// Response-loss replay reuses the accepted job, even after batches advanced.
		} else {
			if in.Revision != task.Revision {
				v1Fail(w, r, 409, "revision_conflict", "任务已更新，请刷新后继续", false)
				return true
			}
			if !exists && len(q.state.Jobs) >= 2000 {
				v1Fail(w, r, 503, "queue_full", "发放队列已满", true)
				return true
			}
			next = queuedMail{ID: task.ID, InitialRevision: task.Revision, Revision: task.Revision, Status: "active", Retry: in.Retry}
			if task.Pending == 0 && (!in.Retry || task.Failed == 0) {
				next.Status = "completed"
			}
		}
	}
	q.state.Jobs[task.ID] = next
	if e = q.save(); e != nil {
		if exists {
			q.state.Jobs[task.ID] = old
		} else {
			delete(q.state.Jobs, task.ID)
		}
		v1Fail(w, r, 503, "queue_unavailable", "保存发放任务失败，请重试", true)
		return true
	}
	var data map[string]any
	json.Unmarshal(raw, &data)
	data["schedule"] = next.Status
	v1Reply(w, r, 200, v1Envelope{Data: data})
	return true
}
func (q *ExternalQueue) step(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, j := range q.state.Jobs {
		if j.Status != "active" {
			continue
		}
		task, _, e := q.task(ctx, id)
		if e != nil {
			return e
		}
		if task.Revision != j.Revision && !(j.InFlight && task.Revision == j.Revision+1) {
			j.Status = "conflict"
			q.state.Jobs[id] = j
			return q.save()
		}
		if task.Revision != j.Revision {
			j.Revision = task.Revision
			j.Retry = false
			j.InFlight = false
		}
		if task.Pending == 0 && (!j.Retry || task.Failed == 0) {
			j.Status = "completed"
			q.state.Jobs[id] = j
			return q.save()
		}
		// Save the uncertain interval before a request can commit on the peer.
		j.InFlight = true
		q.state.Jobs[id] = j
		if e = q.save(); e != nil {
			return e
		}
		var result struct {
			Data queueTask `json:"data"`
		}
		e = q.peer.JSON(ctx, "/api/v1/tasks/"+id+"/run", map[string]any{"revision": j.Revision, "confirm": true, "retry": j.Retry}, &result)
		if e != nil {
			return e
		}
		if result.Data.ID != id || result.Data.Revision != j.Revision+1 {
			return errors.New("queued task response mismatch")
		}
		j.Revision = result.Data.Revision
		j.InFlight = false
		j.Retry = false
		if result.Data.Pending == 0 {
			j.Status = "completed"
		}
		q.state.Jobs[id] = j
		return q.save()
	}
	return nil
}
func (q *ExternalQueue) Run(ctx context.Context, log func(string, ...any)) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if e := q.stepBenefits(ctx); e != nil && ctx.Err() == nil {
				log("external-benefit-queue-retry")
			}
			if e := q.step(ctx); e != nil && ctx.Err() == nil {
				log("external-mail-queue-retry")
			}
		}
	}
}
