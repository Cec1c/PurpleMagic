package gmtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"dnf90.local/gateway/internal/servicehttp"
)

type queuedBenefit struct {
	ID         string                     `json:"id"`
	CampaignID string                     `json:"campaignId"`
	Revision   uint64                     `json:"revision"`
	Status     string                     `json:"status"`
	StartedAt  time.Time                  `json:"startedAt"`
	UpdatedAt  time.Time                  `json:"updatedAt"`
	Cursor     string                     `json:"cursor"`
	Checked    int                        `json:"checked"`
	Received   int                        `json:"received"`
	Skipped    int                        `json:"skipped"`
	Failed     int                        `json:"failed"`
	Failures   []benefitRedeliveryFailure `json:"failures,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

func (q *ExternalQueue) latestBenefit(id string) *queuedBenefit {
	var result *queuedBenefit
	for _, j := range q.state.Benefits {
		if j.CampaignID == id && (result == nil || j.StartedAt.After(result.StartedAt)) {
			copy := j
			result = &copy
		}
	}
	return result
}

// Scheduling only verifies the rule and durably records confirmation. The
// worker enumerates recipients later, independently of the browser connection.
func (q *ExternalQueue) HandleBenefit(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/benefits/"), "/")
	if len(parts) != 2 || parts[1] != "redelivery" {
		return false
	}
	id := parts[0]
	if !queueID(id) {
		v1Fail(w, r, 404, "not_found", "规则不存在", false)
		return true
	}
	if r.Method == "GET" {
		q.mu.Lock()
		j := q.latestBenefit(id)
		q.mu.Unlock()
		v1Reply(w, r, 200, v1Envelope{Data: j})
		return true
	}
	if r.Method != "POST" {
		v1Fail(w, r, 405, "method_not_allowed", "请求方法无效", false)
		return true
	}
	var in struct {
		Revision uint64 `json:"revision"`
		Confirm  bool   `json:"confirm"`
	}
	if !v1Decode(w, r, &in) {
		return true
	}
	key, ok := v1Idempotency(w, r)
	if !ok {
		return true
	}
	if !in.Confirm {
		v1Fail(w, r, 422, "confirmation_required", "请确认补发范围", false)
		return true
	}
	hash := sha256.Sum256([]byte("benefit-redelivery:" + key))
	jobID := hex.EncodeToString(hash[:16])
	q.mu.Lock()
	previous, exists := q.state.Benefits[jobID]
	q.mu.Unlock()
	if exists {
		if previous.CampaignID != id || previous.Revision != in.Revision {
			v1Fail(w, r, 409, "idempotency_conflict", "请求编号已用于其他补发操作", false)
		} else {
			v1Reply(w, r, 200, v1Envelope{Data: previous})
		}
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	res, err := q.peer.Do(ctx, "GET", "/api/v1/benefits/"+id, nil, nil)
	if err != nil {
		v1Fail(w, r, 503, "gateway_unavailable", "读取福利规则失败，请重试", true)
		return true
	}
	defer res.Body.Close()
	var rule struct {
		Data struct {
			ID          string
			Revision    uint64
			Enabled     bool
			PVFChecksum string
		}
	}
	if res.StatusCode != 200 || res.Header.Get("X-PurpleMagic-Protocol") != servicehttp.Version || res.Header.Get("X-PVF-SHA256") != q.peer.Hash() || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rule) != nil || rule.Data.ID != id {
		v1Fail(w, r, 503, "gateway_unavailable", "读取福利规则失败，请重试", true)
		return true
	}
	if !rule.Data.Enabled || rule.Data.Revision != in.Revision || (rule.Data.PVFChecksum != "" && rule.Data.PVFChecksum != q.peer.Hash()) {
		v1Fail(w, r, 409, "rule_changed", "请启用规则并核对当前版本后补发", false)
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	// Another request can have scheduled this rule while its read was in flight.
	if prior, ok := q.state.Benefits[jobID]; ok {
		if prior.CampaignID != id || prior.Revision != in.Revision {
			v1Fail(w, r, 409, "idempotency_conflict", "请求编号冲突", false)
		} else {
			v1Reply(w, r, 200, v1Envelope{Data: prior})
		}
		return true
	}
	if j := q.latestBenefit(id); j != nil && j.Status == "active" {
		v1Fail(w, r, 409, "redelivery_active", "此规则正在后台补发，请查看发放详情", false)
		return true
	}
	if len(q.state.Benefits) >= 1000 {
		v1Fail(w, r, 503, "queue_full", "补发记录已满", false)
		return true
	}
	now := time.Now().UTC()
	j := queuedBenefit{ID: jobID, CampaignID: id, Revision: in.Revision, Status: "active", StartedAt: now, UpdatedAt: now}
	q.state.Benefits[jobID] = j
	if err = q.save(); err != nil {
		delete(q.state.Benefits, jobID)
		v1Fail(w, r, 503, "queue_unavailable", "保存补发任务失败，请重试", true)
		return true
	}
	v1Reply(w, r, 200, v1Envelope{Data: j})
	return true
}

func (q *ExternalQueue) stepBenefits(ctx context.Context) error {
	q.benefitMu.Lock()
	defer q.benefitMu.Unlock()
	q.mu.Lock()
	jobs := []queuedBenefit{}
	for _, j := range q.state.Benefits {
		if j.Status == "active" {
			jobs = append(jobs, j)
		}
	}
	q.mu.Unlock()
	// Fair ordering across rules. No network operation holds the status mutex.
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].UpdatedAt.Before(jobs[j].UpdatedAt) })
	if len(jobs) == 0 {
		return nil
	}
	j := jobs[0]
	old := j
	var out benefitRedeliveryResult
	run, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := q.peer.JSON(run, "/api/internal/gateway/v1/benefits/"+j.CampaignID+"/redelivery", benefitRedeliveryInput{j.Revision, j.StartedAt, j.Cursor}, &out)
	cancel()
	j.UpdatedAt = time.Now().UTC()
	if err != nil {
		j.Error = "连接或读取失败，后台正在重试"
	} else if out.Checked < 0 || out.Checked > 1 || out.Received < 0 || out.Skipped < 0 || out.Failed < 0 || out.Received+out.Skipped+out.Failed > out.Checked || len(out.Next) > 256 {
		return errors.New("invalid benefit batch response")
	} else {
		j.Error = out.Stop
		j.Cursor = out.Next
		j.Checked += out.Checked
		j.Received += out.Received
		j.Skipped += out.Skipped
		j.Failed += out.Failed
		for _, failure := range out.Failures {
			if len(j.Failures) < 10 {
				j.Failures = append(j.Failures, failure)
			}
		}
		if out.Stop != "" {
			j.Status = "conflict"
		} else if out.Next == "" {
			j.Status = "completed"
			if j.Failed > 0 {
				j.Status = "failed"
			}
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.state.Benefits[j.ID] = j
	if e := q.save(); e != nil {
		q.state.Benefits[j.ID] = old
		return e
	}
	return err
}
