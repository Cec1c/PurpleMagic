package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/servicehttp"
)

func TestBenefitReconciliationDurableCadence(t *testing.T) {
	ctx := context.Background()
	hash, token := strings.Repeat("a", 64), strings.Repeat("t", 48)
	var mu sync.Mutex
	seen := []benefitScanPosition{}
	lost := true
	next := benefitScanPosition{Account: 5, Character: "100", Campaign: strings.Repeat("b", 32)}
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var in benefitScanPosition
		json.NewDecoder(r.Body).Decode(&in)
		seen = append(seen, in)
		if lost {
			lost = false
			http.Error(w, "lost response", 503)
			return
		}
		out := benefitScanResult{Position: next, Checked: 2, Received: 1, Delivered: 1}
		if in == next {
			out = benefitScanResult{Done: true, Checked: 1, Received: 1}
		}
		json.NewEncoder(w).Encode(out)
	})))
	defer server.Close()
	peer, err := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "queue.json")
	open := func() *ExternalQueue {
		q, e := OpenExternalQueue(path, peer)
		if e != nil {
			t.Fatal(e)
		}
		q.ConfigureBenefitReconciliation(true)
		return q
	}
	q := open()
	now := time.Now().UTC()
	if _, e := q.stepBenefitReconciliation(ctx, now, nil); e == nil {
		t.Fatal("response loss not exercised")
	}
	q = open()
	if more, e := q.stepBenefitReconciliation(ctx, now.Add(time.Second), nil); e != nil || !more {
		t.Fatal(more, e)
	}
	q = open()
	if q.state.Reconciliation.Position != next {
		t.Fatal("checkpoint lost")
	}
	if more, e := q.stepBenefitReconciliation(ctx, now.Add(2*time.Second), nil); e != nil || more {
		t.Fatal(more, e)
	}
	finished := *q.state.Reconciliation
	if finished.Status != "waiting" || finished.Checked != 3 || finished.Delivered != 1 || finished.NextAt.Sub(finished.CompletedAt) != 3*time.Hour {
		t.Fatal(finished)
	}
	q = open()
	if _, e := q.stepBenefitReconciliation(ctx, finished.NextAt.Add(-time.Nanosecond), nil); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	if len(seen) != 3 || seen[0] != seen[1] || seen[2] != next {
		t.Fatal(seen)
	}
	mu.Unlock()
	if _, e := q.stepBenefitReconciliation(ctx, finished.NextAt, nil); e != nil {
		t.Fatal(e)
	}
	// A newly enabled rule while the sweep is in flight must schedule another
	// whole pass, even if its recipients are before the current cursor.
	if e := q.RequestBenefitReconciliation(); e != nil {
		t.Fatal(e)
	}
	if _, e := q.stepBenefitReconciliation(ctx, finished.NextAt.Add(time.Second), nil); e != nil {
		t.Fatal(e)
	}
	if q.state.Reconciliation.NextAt.After(finished.NextAt.Add(time.Second)) {
		t.Fatal("rule activation lost")
	}
	q.ConfigureBenefitReconciliation(false)
	before := q.state.Reconciliation.Pages
	q.stepBenefitReconciliation(ctx, finished.NextAt.Add(4*time.Hour), nil)
	if q.state.Reconciliation.Pages != before {
		t.Fatal("disabled worker ran")
	}
	// An archive change restarts the sweep and retains existing queue records.
	peer2, _ := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: strings.Repeat("c", 64)})
	q, e := OpenExternalQueue(path, peer2)
	if e != nil || q.state.Reconciliation != nil {
		t.Fatal("new PVF kept stale cursor", e)
	}
}

func TestBenefitReconciliationRequestDuringPageAndPrivateStatus(t *testing.T) {
	hash, token := strings.Repeat("a", 64), strings.Repeat("t", 48)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(benefitScanResult{Done: true})
	})))
	defer server.Close()
	peer, _ := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	q, e := OpenExternalQueue(filepath.Join(t.TempDir(), "queue.json"), peer)
	if e != nil {
		t.Fatal(e)
	}
	q.ConfigureBenefitReconciliation(true)
	done := make(chan error, 1)
	now := time.Now().UTC()
	go func() { _, err := q.stepBenefitReconciliation(context.Background(), now, nil); done <- err }()
	<-entered
	if e = q.RequestBenefitReconciliation(); e != nil {
		t.Fatal(e)
	}
	status := httptest.NewRecorder()
	q.HandleBenefitReconciliation(status, httptest.NewRequest("GET", "/api/v1/benefits/reconciliation", nil))
	if status.Code != 200 || strings.Contains(status.Body.String(), "position") {
		t.Fatal(status.Body.String())
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if q.state.Reconciliation.NextAt.After(now) {
		t.Fatal("concurrent request overwritten")
	}
	s := &Server{cfg: Config{Password: strings.Repeat("p", 48)}}
	// ExternalHandler must authenticate before serving scheduler status.
	w := httptest.NewRecorder()
	s.ExternalHandler("", "", peer, q).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/benefits/reconciliation", nil))
	if w.Code != 401 {
		t.Fatal("status exposed without login", w.Code)
	}
}
