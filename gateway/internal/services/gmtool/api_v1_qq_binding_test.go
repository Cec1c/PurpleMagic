package gmtool

import (
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQQBindingChangeAuthenticationAndExactRetry(t *testing.T) {
	s, _, token := activityV1Fixture(t)
	var calls []adminapi.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in adminapi.Request
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, in)
		json.NewEncoder(w).Encode(adminapi.Response{Version: 1, OK: true, QQChanged: true})
	}))
	defer upstream.Close()
	client, err := adminapi.NewClient(upstream.URL+adminapi.Path, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.deps.Runtime = client
	body := `{"accountId":"fixture:main","qq":"234567890","revision":3,"action":"delete","requestId":"binding-change-00001"}`
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"Sec-Fetch-Site": "cross-site"}} {
		if status, _ := operationCall(s, token, "anti-cheat/qq/bindings", body, "", headers); status != 403 {
			t.Fatal(status)
		}
	}
	if status, _ := operationCall(s, "", "anti-cheat/qq/bindings", body, "", nil); status != 401 {
		t.Fatal(status)
	}
	if len(calls) != 0 {
		t.Fatal("unauthenticated mutation")
	}
	for i := 0; i < 2; i++ {
		if status, out := operationCall(s, token, "anti-cheat/qq/bindings", body, "", nil); status != 200 {
			t.Fatal(status, out)
		}
	}
	if len(calls) != 2 || calls[0].Action != "qq_change" || *calls[0].QQChange != *calls[1].QQChange {
		t.Fatal(calls)
	}
	if status, _ := operationCall(s, token, "anti-cheat/qq/bindings", strings.Replace(body, `"revision":3`, `"revision":0`, 1), "", nil); status != 400 {
		t.Fatal(status)
	}
}
