package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/onlineevent"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository/memory"
)

func activityV1Fixture(t *testing.T) (*Server, *onlineevent.Controls, string) {
	t.Helper()
	controls, err := onlineevent.NewControls(context.Background(), memory.NewMemoryGroup().Settings, []onlineevent.ControlDefinition{
		{ID: 145, Name: "骰子", DefaultEnabled: true}, {ID: 2395, Name: "乔纳森", DefaultEnabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{Password: strings.Repeat("p", 48), Logger: func(string, ...any) {}}, deps: Deps{Activities: controls}, www: http.NotFoundHandler(), sessions: map[string]time.Time{}}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	return s, controls, token
}

func TestActivityV1AuthenticationValidationAndAtomicSave(t *testing.T) {
	s, controls, token := activityV1Fixture(t)
	body := `{"revision":0,"activities":[{"id":145,"enabled":false},{"id":2395,"enabled":true}]}`
	if code, _ := operationCall(s, "", "activities", body, "", nil); code != 401 {
		t.Fatal(code)
	}
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"Sec-Fetch-Site": "cross-site"}} {
		if code, _ := operationCall(s, token, "activities", body, "", headers); code != 403 {
			t.Fatal(code)
		}
	}
	for _, invalid := range []string{
		`null`, `{}`, `{"revision":0,"activities":[]}`, `{"revision":0,"activities":[{"id":145}]}`,
		`{"revision":0,"activities":[{"id":145,"enabled":null}]}`,
		`{"revision":0,"activities":[{"id":145,"enabled":"false"}]}`,
		`{"revision":0,"activities":[{"id":145,"enabled":false},{"id":145,"enabled":true}]}`,
		`{"revision":0,"activities":[{"id":145,"enabled":false},{"id":65535,"enabled":true}]}`,
		`{"revision":0,"activities":[{"id":0,"enabled":false}]}`,
		`{"revision":-1,"activities":[{"id":145,"enabled":false}]}`,
		`{"revision":0,"activities":[{"id":145,"enabled":false,"reward":1}]}`,
		body + ` {}`, `{"revision":0,"activities":[` + strings.Repeat(`{"id":145,"enabled":false},`, 256) + `{"id":2395,"enabled":true}]}`,
	} {
		if code, out := operationCall(s, token, "activities", invalid, "", nil); code != 400 {
			t.Fatalf("invalid accepted: %d %v", code, out)
		}
		if controls.Snapshot().Revision != 0 {
			t.Fatal("invalid batch partially saved")
		}
	}
	if code, _ := operationCall(s, token, "activities?unexpected=1", body, "", nil); code != 400 {
		t.Fatal(code)
	}
	code, out := operationCall(s, token, "activities", body, "", nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	snapshot := controls.Snapshot()
	if snapshot.Revision != 1 || !snapshot.RestartRequired || !controls.Enabled(145) || controls.Enabled(2395) {
		t.Fatal(snapshot)
	}
	if snapshot.Activities[0].Enabled || !snapshot.Activities[1].Enabled {
		t.Fatal(snapshot)
	}
	if code, _ = operationCall(s, token, "activities", body, "", nil); code != 409 {
		t.Fatal("stale save", code)
	}
	if code, _ = operationCall(s, token, "activities", strings.Replace(body, `"revision":0`, `"revision":1`, 1), "", nil); code != 200 || controls.Snapshot().Revision != 1 {
		t.Fatal("unchanged save advanced version")
	}
	if code, out = v1TestCall(t, s, token, "GET", "/api/v1/activities"); code != 200 || out["data"].(map[string]any)["revision"] != float64(1) {
		t.Fatal(code, out)
	}
}

func TestActivityV1UnchangedRowKeepsDefaultPolicy(t *testing.T) {
	s, controls, token := activityV1Fixture(t)
	code, out := operationCall(s, token, "activities", `{"revision":0,"activities":[{"id":145,"enabled":false},{"id":2395,"enabled":false}]}`, "", nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	rows := controls.Snapshot().Activities
	if !rows[0].PendingRestart || rows[1].PendingRestart {
		t.Fatal("unchanged default policy became an explicit override", rows)
	}
}

func TestActivityV1RuntimeSaveConflictAndLostResponse(t *testing.T) {
	for _, mode := range []string{"saved", "concurrent", "lost-response", "failure"} {
		t.Run(mode, func(t *testing.T) {
			s, controls, token := activityV1Fixture(t)
			s.deps.Activities = nil
			saves := 0
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in adminapi.Request
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path != adminapi.Path || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
					t.Error("private runtime identity lost")
				}
				var result onlineevent.ControlSnapshot
				var err error
				switch in.Action {
				case "activities":
					result = controls.Snapshot()
				case "save_activities":
					saves++
					if mode == "failure" {
						http.Error(w, "fixture unavailable", 503)
						return
					}
					if mode == "concurrent" {
						_, _ = controls.Save(r.Context(), 0, []onlineevent.ControlChange{{ID: 2395, Enabled: true}})
					}
					result, err = controls.Save(r.Context(), in.Revision, in.Changes)
					if mode == "lost-response" {
						http.Error(w, "reply unavailable", 503)
						return
					}
				default:
					t.Error("unexpected game operation", in.Action)
				}
				response := adminapi.Response{Version: 1, OK: err == nil, Activities: &result}
				if err != nil {
					response.Error = err.Error()
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer runtime.Close()
			s.deps.Runtime, _ = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			code, out := operationCall(s, token, "activities", `{"revision":0,"activities":[{"id":145,"enabled":false}]}`, "", nil)
			want := 200
			if mode == "concurrent" || mode == "lost-response" {
				want = 409
			}
			if mode == "failure" {
				want = 503
			}
			if code != want || saves != 1 {
				t.Fatal(code, out, saves)
			}
			if mode == "concurrent" && !controls.Snapshot().Activities[0].Enabled {
				t.Fatal("concurrent configuration overwritten")
			}
			if !controls.Enabled(145) {
				t.Fatal("runtime activity changed without restart")
			}
		})
	}
}

func TestActivityV1ExternalManagementIsolation(t *testing.T) {
	s, controls, _ := activityV1Fixture(t)
	source := questFixture("任务")
	s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
	s.items = newItemIndex("", s, s.cfg.Logger)
	s.items.setEntriesLocked(itemcfgTestEntries())
	cat, peer, calls := externalTestServices(t, s)
	token, _ := cat.startSession()
	handler := cat.ExternalHandler("", "", peer)
	request := func(csrf bool) int {
		r := httptest.NewRequest("POST", "/api/v1/activities", strings.NewReader(`{"revision":0,"activities":[{"id":145,"enabled":false}]}`))
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		r.Header.Set("Content-Type", "application/json")
		if csrf {
			r.Header.Set("X-CSRF-Token", cat.v1CSRF(r))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := request(false); code != 403 || calls.Load() != 0 {
		t.Fatal(code, calls.Load())
	}
	if code := request(true); code != 200 || calls.Load() != 1 {
		t.Fatal(code, calls.Load())
	}
	if controls.Snapshot().Revision != 1 || !controls.Enabled(145) {
		t.Fatal(controls.Snapshot())
	}
	code, out := v1TestCall(t, s, "", "GET", "/api/v1/activities")
	if code != 401 {
		t.Fatal("management authentication lost", code, out)
	}
}
