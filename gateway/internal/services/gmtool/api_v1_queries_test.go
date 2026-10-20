package gmtool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/onlineevent"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	platformpvf "dnf90.local/gateway/internal/platform/pvf"
)

type modeTestSource struct {
	*itemCacheTestSource
	mu   sync.Mutex
	mode string
}

func (s *modeTestSource) ModeStatus() platformpvf.ModeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return platformpvf.ModeStatus{Mode: s.mode, Desired: s.mode, Checksum: s.checksum, ArchiveBytes: 120000000}
}
func (s *modeTestSource) SetMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	return nil
}

func TestV1QueriesAuthenticationIsolationAndActivityStates(t *testing.T) {
	source := &modeTestSource{itemCacheTestSource: questFixture("任务"), mode: "disk"}
	s := &Server{cfg: Config{Password: strings.Repeat("p", 48), Logger: func(string, ...any) {}}, sessions: map[string]time.Time{}, failures: map[string][]time.Time{}, www: http.NotFoundHandler(), deps: Deps{PVF: func() (dnfpvf.Source, error) { return source, nil }}}
	s.items = newItemIndex("", s, s.cfg.Logger)
	s.items.setEntriesLocked(itemcfgTestEntries())
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in adminapi.Request
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Action != "activities" {
			t.Error("unexpected runtime action", in.Action, err)
		}
		json.NewEncoder(w).Encode(adminapi.Response{Version: 1, OK: true, Activities: &onlineevent.ControlSnapshot{Revision: 4, RestartRequired: true, Activities: []onlineevent.ControlRow{{ControlDefinition: onlineevent.ControlDefinition{ID: 2395, Name: "活动"}, Enabled: true, EffectiveEnabled: false, PendingRestart: true}}}})
	}))
	defer runtime.Close()
	var err error
	s.deps.Runtime, err = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	token, _ := s.startSession()
	for _, path := range []string{"/api/v1/activities", "/api/v1/settings/pvf"} {
		if code, _ := v1TestCall(t, s, "", "GET", path); code != 401 {
			t.Fatal(code)
		}
		if code, _ := v1TestCall(t, s, token, "GET", path+"?unexpected=1"); code != 400 {
			t.Fatal(code)
		}
	}
	code, out := v1TestCall(t, s, token, "GET", "/api/v1/activities")
	if code != 200 {
		t.Fatal(code, out)
	}
	row := out["data"].(map[string]any)["activities"].([]any)[0].(map[string]any)
	if row["enabled"] != true || row["effectiveEnabled"] != false || row["pendingRestart"] != true {
		t.Fatal(row)
	}
	runtime.Close()
	if code, _ := v1TestCall(t, s, token, "GET", "/api/v1/activities"); code != 503 {
		t.Fatal(code)
	}
	cat, peer, calls := externalTestServices(t, s)
	catToken, _ := cat.startSession()
	handler := cat.ExternalHandler("", "", peer)
	request := func(method, body, csrf string) int {
		r := httptest.NewRequest(method, "/api/v1/settings/pvf", strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: CookieName, Value: catToken})
		r.Header.Set("Content-Type", "application/json")
		if csrf == "valid" {
			r.Header.Set("X-CSRF-Token", cat.v1CSRF(r))
		} else {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if request("POST", `{"mode":"memory"}`, "") != 403 || source.ModeStatus().Mode != "disk" {
		t.Fatal("CSRF not enforced")
	}
	if code := request("POST", `{"mode":"bad"}`, "valid"); code != 400 {
		t.Fatal(code)
	}
	if code := request("POST", `{"mode":"memory"}`, "valid"); code != 200 || source.ModeStatus().Mode != "memory" {
		t.Fatal(code)
	}
	if request("GET", "", "") != 200 || calls.Load() != 0 {
		t.Fatal("local mode request forwarded")
	}
	res, e := peer.Do(t.Context(), "GET", "/api/v1/settings/pvf", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("game gateway exposes local setting", res.StatusCode)
	}
}
