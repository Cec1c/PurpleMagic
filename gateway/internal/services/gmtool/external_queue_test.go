package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"dnf90.local/gateway/internal/servicehttp"
)

func TestV1ExternalQueueSaveFailureClassifiesDiskFull(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"disk full", &os.PathError{Op: "write", Path: "/private/queue", Err: syscall.ENOSPC}, "queue_storage_full"},
		{"permission", &os.PathError{Op: "write", Path: "/private/queue", Err: os.ErrPermission}, "queue_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/v1/tasks/test/schedule", nil)
			queueSaveFailure(w, r, tc.err, "保存发放任务失败，请重试")
			if w.Code != 503 || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "/private/") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestV1ExternalQueueFailedSaveDoesNotStartDelivery(t *testing.T) {
	id, hash, token := strings.Repeat("1", 32), strings.Repeat("a", 64), strings.Repeat("t", 48)
	writes := 0
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id, "revision": 0, "pending": 1, "pvfChecksum": hash}})
	})))
	defer server.Close()
	peer, _ := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := OpenExternalQueue(path, peer)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	q.path = filepath.Join(path, "not-a-directory.json")
	w := httptest.NewRecorder()
	q.Handle(w, httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/schedule", strings.NewReader(`{"revision":0,"confirm":true}`)))
	if w.Code != 503 || q.Status(id) != "" {
		t.Fatal(w.Code, w.Body.String(), q.Status(id))
	}
	if err := q.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if writes != 0 || string(before) != string(after) {
		t.Fatal("failed persistence changed delivery or durable queue")
	}
	q.path = path
	w = httptest.NewRecorder()
	q.Handle(w, httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/schedule", strings.NewReader(`{"revision":0,"confirm":true}`)))
	if w.Code != 200 || q.Status(id) != "active" || writes != 0 {
		t.Fatal("same task did not recover after storage repair", w.Code, w.Body.String())
	}
}

func TestV1ExternalQueueOldTaskPVFRequiresNewPreview(t *testing.T) {
	id, hash, token := strings.Repeat("1", 32), strings.Repeat("a", 64), strings.Repeat("t", 48)
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("old task attempted a write")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id, "revision": 0, "pending": 1, "pvfChecksum": strings.Repeat("b", 64)}})
	})))
	defer server.Close()
	peer, _ := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	q, err := OpenExternalQueue(filepath.Join(t.TempDir(), "queue.json"), peer)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	q.Handle(w, httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/schedule", strings.NewReader(`{"revision":0,"confirm":true}`)))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "pvf_conflict") || q.Status(id) != "" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestV1ExternalQueueRestartAfterCommittedResponseLoss(t *testing.T) {
	id, hash, token := strings.Repeat("1", 32), strings.Repeat("a", 64), strings.Repeat("t", 48)
	revision, pending, grants := uint64(1), 15, 0
	lost := true
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks/"+id && r.URL.Path != "/api/v1/tasks/"+id+"/run" {
			t.Error("unexpected route", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" {
			var in struct {
				Revision uint64
				Confirm  bool
			}
			json.NewDecoder(r.Body).Decode(&in)
			if in.Revision != revision || !in.Confirm {
				http.Error(w, "revision conflict", 409)
				return
			}
			n := min(pending, 10)
			pending -= n
			grants += n
			revision++
			if lost {
				lost = false
				http.Error(w, "committed response lost", 503)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id, "revision": revision, "pending": pending, "sent": grants, "total": 15, "pvfChecksum": hash}})
	})))
	defer server.Close()
	peer, e := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "queue.json")
	q, e := OpenExternalQueue(path, peer)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/schedule", strings.NewReader(`{"revision":1,"confirm":true}`))
	w := httptest.NewRecorder()
	if !q.Handle(w, r) || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if grants != 0 {
		t.Fatal("confirmation handler executed before persistence")
	}
	if e = q.step(context.Background()); e == nil || grants != 10 {
		t.Fatal("lost response not exercised", e, grants)
	}
	restored, e := OpenExternalQueue(path, peer)
	if e != nil {
		t.Fatal(e)
	}
	if e = restored.step(context.Background()); e != nil {
		t.Fatal(e)
	}
	if grants != 15 || pending != 0 || restored.Status(id) != "completed" {
		t.Fatal("restart lost receipt progress", grants, restored.Status(id))
	}
	if e = restored.step(context.Background()); e != nil || grants != 15 {
		t.Fatal("duplicate grant", e, grants)
	}
	// An unreviewed revision cannot start another run.
	r = httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/schedule", strings.NewReader(`{"revision":2,"confirm":true}`))
	w = httptest.NewRecorder()
	restored.Handle(w, r)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestV1ExternalQueuePauseAndGenerationChange(t *testing.T) {
	hash, token, id := strings.Repeat("a", 64), strings.Repeat("t", 48), strings.Repeat("1", 32)
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id, "revision": 1, "pending": 10, "pvfChecksum": hash}})
	})))
	defer server.Close()
	peer, _ := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	path := filepath.Join(t.TempDir(), "queue.json")
	q, e := OpenExternalQueue(path, peer)
	if e != nil {
		t.Fatal(e)
	}
	for _, action := range []string{"schedule", "pause"} {
		r := httptest.NewRequest("POST", "/api/v1/tasks/"+id+"/"+action, strings.NewReader(`{"revision":1,"confirm":true}`))
		w := httptest.NewRecorder()
		q.Handle(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if q.Status(id) != "paused" {
		t.Fatal(q.Status(id))
	}
	restored, e := OpenExternalQueue(path, peer)
	if e != nil || restored.Status(id) != "paused" {
		t.Fatal(e)
	}
	q.state.Jobs[id] = queuedMail{ID: id, Revision: 1, Status: "active"}
	if e = q.save(); e != nil {
		t.Fatal(e)
	}
	nextPeer, _ := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: strings.Repeat("b", 64)})
	restored, e = OpenExternalQueue(path, nextPeer)
	if e != nil || restored.Status(id) != "conflict" {
		t.Fatal("old generation resumed", e)
	}
}
