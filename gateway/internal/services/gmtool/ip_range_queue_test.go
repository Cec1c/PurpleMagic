package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/servicehttp"
)

func TestIPRangePreview(t *testing.T) {
	for _, s := range []string{"192.0.2.*", "192.0.2.0/24", " 192.0.2.99/24 "} {
		p, e := previewIPRange(s)
		if e != nil || p.Target != "192.0.2.0/24" || p.Count != 256 || p.First != "192.0.2.0" || p.Last != "192.0.2.255" {
			t.Fatal(s, p, e)
		}
	}
	for _, s := range []string{"192.0.*.1", "192.0.2.256/24", "192.0.*.*", "0.0.0.0/0", "192.0.2.0/33", "2001:db8::/64", "192.0.02.*", "192.0.2.1", "/24", "192.0.2.0/24;delete"} {
		if _, e := previewIPRange(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
	if p, e := previewIPRange("192.0.2.12/32"); e != nil || p.Count != 1 || p.Last != p.First {
		t.Fatal(p, e)
	}
	if p, e := previewIPRange("192.0.0.0/20"); e != nil || p.Count != 4096 {
		t.Fatal(p, e)
	}
}

type rangeFixture struct {
	mu       sync.Mutex
	q        *ipRangeQueue
	bans     map[string]moderation.Ban
	receipts map[string]moderation.Ban
	commands map[string]moderation.BanChange
	writes   int
	lost     bool
}

func newRangeFixture(t *testing.T) *rangeFixture {
	t.Helper()
	f := &rangeFixture{bans: map[string]moderation.Ban{}, receipts: map[string]moderation.Ban{}, commands: map[string]moderation.BanChange{}}
	hash, token := strings.Repeat("a", 64), strings.Repeat("t", 48)
	server := httptest.NewServer(servicehttp.Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/api/v1/anti-cheat/bans" {
			t.Error(r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == "GET" {
			all := []moderation.Ban{}
			for _, b := range f.bans {
				all = append(all, b)
			}
			sort.Slice(all, func(i, j int) bool { return all[i].Target < all[j].Target })
			o, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit == 0 {
				limit = 50
			}
			if o > len(all) {
				o = len(all)
			}
			end := min(o+limit, len(all))
			json.NewEncoder(w).Encode(map[string]any{"data": moderation.BanPage{Rows: all[o:end], HasMore: end < len(all)}})
			return
		}
		var c moderation.BanChange
		if json.NewDecoder(r.Body).Decode(&c) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if p, ok := f.receipts[c.RequestID]; ok {
			a, _ := json.Marshal(c)
			b, _ := json.Marshal(f.commands[c.RequestID])
			if string(a) != string(b) {
				http.Error(w, "conflict", 409)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": p})
			return
		}
		if e := c.Normalize(time.Now()); e != nil {
			http.Error(w, "bad command", 400)
			return
		}
		prior := f.bans[c.Target]
		if prior.Revision != c.Revision {
			http.Error(w, "conflict", 409)
			return
		}
		b := moderation.Ban{Kind: c.Kind, Target: c.Target, Label: c.Target, Reason: c.Reason, ExpiresAt: c.ExpiresAt, Revoked: c.Revoke, Active: !c.Revoke, Revision: prior.Revision + 1, UpdatedAt: time.Now()}
		f.writes++
		f.bans[c.Target] = b
		f.receipts[c.RequestID] = b
		f.commands[c.RequestID] = c
		if f.lost {
			f.lost = false
			http.Error(w, "committed response lost", 503)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": b})
	})))
	t.Cleanup(server.Close)
	peer, e := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
	if e != nil {
		t.Fatal(e)
	}
	f.q, e = openIPRangeQueue(filepath.Join(t.TempDir(), "ranges.json"), peer)
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func finishRanges(t *testing.T, q *ipRangeQueue) {
	t.Helper()
	for i := 0; i < 10000; i++ {
		worked, e := q.step(context.Background())
		if e != nil {
			var pe *servicehttp.PeerError
			if !errors.As(e, &pe) || pe.Status != 409 {
				t.Fatal(e)
			}
		}
		if !worked {
			return
		}
	}
	t.Fatal("worker did not finish")
}
func TestIPRangeExpandedOncePersistsAndRecoversLostResponse(t *testing.T) {
	f := newRangeFixture(t)
	in := ipRangeInput{Target: "192.0.2.*", Reason: "fixture", RequestID: "range-request-000001"}
	v, e := f.q.create(context.Background(), in)
	if e != nil || v.Count != 256 || f.writes != 0 {
		t.Fatal(v, e, f.writes)
	}
	in.Target = "192.0.2.99/24"
	same, e := f.q.create(context.Background(), in)
	if e != nil || same.ID != v.ID {
		t.Fatal(same, e)
	}
	f.lost = true
	if _, e = f.q.step(context.Background()); e == nil || f.writes != 1 {
		t.Fatal("lost receipt", e, f.writes)
	}
	f.q, e = openIPRangeQueue(f.q.path, f.q.peer)
	if e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	if f.writes != 256 || len(f.receipts) != 256 || len(f.bans) != 256 {
		t.Fatal(f.writes, len(f.receipts), len(f.bans))
	}
	if !f.bans["192.0.2.0"].Active || !f.bans["192.0.2.255"].Active {
		t.Fatal("network boundaries omitted")
	}
	if f.q.jobs[v.ID].view().Status != "active" {
		t.Fatal(f.q.jobs[v.ID].view())
	}
}
func TestIPRangeReleasePreservesPriorRulesAndConcurrentEdits(t *testing.T) {
	f := newRangeFixture(t)
	soon := time.Now().Add(time.Hour)
	later := time.Now().Add(2 * time.Hour)
	f.bans["192.0.2.0"] = moderation.Ban{Kind: "ip", Target: "192.0.2.0", Reason: "permanent", Active: true, Revision: 4}
	f.bans["192.0.2.1"] = moderation.Ban{Kind: "ip", Target: "192.0.2.1", Reason: "original", Active: true, Revision: 8, ExpiresAt: &soon}
	v, e := f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/30", Reason: "range", ExpiresAt: &later, RequestID: "range-request-000002"})
	if e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	if v.Retained != 1 || f.bans["192.0.2.0"].Revision != 4 || f.bans["192.0.2.1"].Revision != 9 {
		t.Fatal(v, f.bans)
	}
	b := f.bans["192.0.2.2"]
	b.Revision++
	b.Reason = "independent edit"
	f.bans[b.Target] = b
	if _, e = f.q.release(v.ID); e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	if f.bans["192.0.2.0"].Revision != 4 || !f.bans["192.0.2.0"].Active {
		t.Fatal("strong prior ban changed")
	}
	restored := f.bans["192.0.2.1"]
	if restored.Reason != "original" || !restored.Active || !restored.ExpiresAt.Equal(soon) {
		t.Fatal(restored)
	}
	if f.bans["192.0.2.2"].Reason != "independent edit" || !f.bans["192.0.2.2"].Active || !f.bans["192.0.2.3"].Revoked {
		t.Fatal(f.bans)
	}
	if f.q.jobs[v.ID].view().Conflict != 1 {
		t.Fatal(f.q.jobs[v.ID].view())
	}
}
func TestIPRangeReleaseDuringUnknownApplyAndOverlap(t *testing.T) {
	f := newRangeFixture(t)
	v, e := f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/30", Reason: "range", RequestID: "range-request-000003"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/31", Reason: "overlap", RequestID: "range-request-000004"}); e == nil {
		t.Fatal("overlap accepted")
	}
	f.lost = true
	f.q.step(context.Background())
	if _, e = f.q.release(v.ID); e != nil {
		t.Fatal(e)
	}
	f.q, e = openIPRangeQueue(f.q.path, f.q.peer)
	if e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	if len(f.bans) != 1 || !f.bans["192.0.2.0"].Revoked || f.writes != 2 {
		t.Fatal(f.writes, f.bans)
	}
	if f.q.jobs[v.ID].view().Status != "released" {
		t.Fatal(f.q.jobs[v.ID].view())
	}
}
func TestIPRangeGroupedListingKeepsChangedRevisionAndPagination(t *testing.T) {
	f := newRangeFixture(t)
	_, e := f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/24", Reason: "range", RequestID: "range-request-000005"})
	if e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	b := f.bans["192.0.2.2"]
	b.Revision++
	f.bans[b.Target] = b
	f.bans["outside"] = moderation.Ban{Kind: "account", Target: "outside", Revision: 1}
	r := httptest.NewRequest("GET", "/api/v1/anti-cheat/bans?hideRangeMembers=true&limit=1&offset=0", nil)
	w := httptest.NewRecorder()
	f.q.handle(w, r)
	var out struct{ Data moderation.BanPage }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Data.Rows) != 1 || !out.Data.HasMore || out.Data.Rows[0].Target != "192.0.2.2" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	f.q.handle(w, httptest.NewRequest("GET", "/api/v1/anti-cheat/bans?hideRangeMembers=true&limit=1&offset=1", nil))
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Data.Rows) != 1 || out.Data.HasMore || out.Data.Rows[0].Kind != "account" {
		t.Fatal(w.Body.String())
	}
}

func TestIPRangeHTTPRequiresSessionAndCSRF(t *testing.T) {
	f := newRangeFixture(t)
	s, _, token := activityV1Fixture(t)
	h := s.ExternalHandler("", "", f.q.peer, &ExternalQueue{ipRanges: f.q})
	body := `{"target":"192.0.2.0/30","reason":"fixture","requestId":"range-request-000006"}`
	for _, tc := range []struct {
		cookie, csrf, cross bool
		want                int
	}{{false, false, false, 401}, {true, false, false, 403}, {true, true, true, 403}, {true, true, false, 200}} {
		r := httptest.NewRequest("POST", "/api/v1/anti-cheat/ip-ranges", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if tc.cookie {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		}
		if tc.csrf {
			r.Header.Set("X-CSRF-Token", s.v1CSRF(r))
		}
		if tc.cross {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if len(f.q.jobs) != 1 || f.writes != 0 {
		t.Fatal("HTTP handler did not only enqueue", len(f.q.jobs), f.writes)
	}
}

func TestIPRangeExpirySkipsUnsentAndStateSaveFailureDoesNotWrite(t *testing.T) {
	f := newRangeFixture(t)
	expiry := time.Now().Add(time.Hour)
	v, e := f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/30", Reason: "range", ExpiresAt: &expiry, RequestID: "range-request-000007"})
	if e != nil {
		t.Fatal(e)
	}
	oldPath := f.q.path
	f.q.path = filepath.Join(t.TempDir(), "missing", "queue.json")
	if _, e = f.q.step(context.Background()); e == nil || f.writes != 0 {
		t.Fatal("write before durable intent", e, f.writes)
	}
	f.q.path = oldPath
	j := f.q.jobs[v.ID]
	past := time.Now().Add(-time.Second)
	j.ExpiresAt = &past
	f.q.jobs[v.ID] = j
	finishRanges(t, f.q)
	if f.writes != 0 || f.q.jobs[v.ID].view().Status != "expired" {
		t.Fatal(f.writes, f.q.jobs[v.ID].view())
	}
}

func TestIPRangeConcurrentRetryWhileWorkerApplies(t *testing.T) {
	f := newRangeFixture(t)
	in := ipRangeInput{Target: "192.0.2.0/27", Reason: "range", RequestID: "range-request-000008"}
	want, e := f.q.create(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			got, e := f.q.create(context.Background(), in)
			if e != nil || got.ID != want.ID {
				t.Errorf("retry: %v, %v", got, e)
				return
			}
		}
	}()
	finishRanges(t, f.q)
	<-done
	if f.writes != 32 {
		t.Fatal("retry duplicated writes", f.writes)
	}
}

func TestIPRangeGroupedListingKeepsReleasedMembersGrouped(t *testing.T) {
	f := newRangeFixture(t)
	expiry := time.Now().Add(time.Hour)
	f.bans["192.0.2.0"] = moderation.Ban{Kind: "ip", Target: "192.0.2.0", Reason: "original", Active: true, Revision: 8, ExpiresAt: &expiry}
	v, e := f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/30", Reason: "range", RequestID: "range-request-000009"})
	if e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	if _, e = f.q.release(v.ID); e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	w := httptest.NewRecorder()
	f.q.handle(w, httptest.NewRequest("GET", "/api/v1/anti-cheat/bans?hideRangeMembers=true", nil))
	var out struct{ Data moderation.BanPage }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Data.Rows) != 1 || out.Data.Rows[0].Reason != "original" {
		t.Fatal("released members should stay grouped; restored prior rule should be visible", w.Body.String())
	}
	v, e = f.q.create(context.Background(), ipRangeInput{Target: "192.0.2.0/30", Reason: "next range", RequestID: "range-request-000010"})
	if e != nil {
		t.Fatal(e)
	}
	finishRanges(t, f.q)
	w = httptest.NewRecorder()
	f.q.handle(w, httptest.NewRequest("GET", "/api/v1/anti-cheat/bans?hideRangeMembers=true", nil))
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Data.Rows) != 0 {
		t.Fatal("latest owned revisions must win across rules", w.Body.String(), v)
	}
}
