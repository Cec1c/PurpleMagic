package gmtool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
)

func TestModerationV1AuthenticationContractAndRecovery(t *testing.T) {
	s, _, token := activityV1Fixture(t)
	var calls []adminapi.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("missing private auth")
		}
		var in adminapi.Request
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		calls = append(calls, in)
		out := adminapi.Response{Version: 1, OK: true}
		switch in.Action {
		case "clear_stats":
			out.Clears = &moderation.ClearPage{Rows: []moderation.ClearStats{}}
		case "change_ban":
			out.Ban = &moderation.Ban{Kind: in.BanChange.Kind, Target: in.BanChange.Target, Revision: 1, Active: true}
		case "bans":
			out.Bans = &moderation.BanPage{Rows: []moderation.Ban{}}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	client, err := adminapi.NewClient(server.URL+adminapi.Path, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.deps.Runtime = client
	body := `{"requestId":"fixture-ban-00000001","kind":"ip","target":"::ffff:192.0.2.1","reason":"fixture","expiresAt":null,"revoke":false,"revision":0}`
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"Sec-Fetch-Site": "cross-site"}} {
		if code, _ := operationCall(s, token, "anti-cheat/bans", body, "", headers); code != 403 {
			t.Fatal(code)
		}
	}
	if code, _ := operationCall(s, "", "anti-cheat/bans", body, "", nil); code != 401 {
		t.Fatal(code)
	}
	if len(calls) != 0 {
		t.Fatal("unauthorized write reached game")
	}
	for _, path := range []string{"/api/v1/anti-cheat/clears?limit=0", "/api/v1/anti-cheat/clears?offset=-1", "/api/v1/anti-cheat/clears?from=x", "/api/v1/anti-cheat/clears?unknown=1", "/api/v1/anti-cheat/clears?sortBy=target", "/api/v1/anti-cheat/bans?sortBy=count", "/api/v1/anti-cheat/accounts?sortOrder=DESC", "/api/v1/anti-cheat/clears?sortBy=count%20DESC"} {
		if code, _ := v1TestCall(t, s, token, "GET", path); code != 400 {
			t.Fatal(path, code)
		}
	}
	if code, out := v1TestCall(t, s, token, "GET", "/api/v1/anti-cheat/clears?limit=50&minHourlyPeak=60&sortBy=fatigueSpent&sortOrder=asc"); code != 200 {
		t.Fatal(code, out)
	}
	for i := 0; i < 2; i++ {
		if code, out := operationCall(s, token, "anti-cheat/bans", body, "", nil); code != 200 {
			t.Fatal(code, out)
		}
	}
	if len(calls) != 3 || calls[0].ModerationQuery.MinHourlyPeak != 60 || calls[0].ModerationQuery.SortBy != "fatigueSpent" || calls[0].ModerationQuery.SortOrder != "asc" || calls[1].BanChange.Target != "192.0.2.1" || *calls[1].BanChange != *calls[2].BanChange {
		t.Fatal(calls)
	}
}
