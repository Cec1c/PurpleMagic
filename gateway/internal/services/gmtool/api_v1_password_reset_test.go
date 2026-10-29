package gmtool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
)

func TestPasswordResetV1AuthenticationAndPrivateContract(t *testing.T) {
	s, _, token := activityV1Fixture(t)
	calls := 0
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("missing private credential")
		}
		var in adminapi.Request
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Action != "reset_account_password" || in.AccountID != "fixture:main" || in.PasswordReset == nil {
			t.Error("invalid forwarding")
			return
		}
		out := adminapi.Response{Version: adminapi.Version, OK: true, PasswordReset: &adminapi.PasswordResetResult{RequestID: in.PasswordReset.RequestID, AccountID: in.AccountID, Account: in.PasswordReset.Account, ResetAt: 12345}}
		switch mode {
		case "conflict":
			out.OK = false
			out.ErrorCode = "reset_superseded"
			out.Error = "该次重置后密码已再次修改"
		case "missing":
			out.PasswordReset = nil
		case "mismatch":
			out.PasswordReset.AccountID = "fixture:other"
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	client, err := adminapi.NewClient(server.URL+adminapi.Path, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.deps.Runtime = client
	body := `{"requestId":"fixture-reset-00000001","account":"main","password":"fixture_password_12345"}`
	path := "accounts/fixture:main/password-reset"
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"Sec-Fetch-Site": "cross-site"}} {
		if code, _ := operationCall(s, token, path, body, "", headers); code != 403 {
			t.Fatal(code)
		}
	}
	if code, _ := operationCall(s, "", path, body, "", nil); code != 401 {
		t.Fatal(code)
	}
	if calls != 0 {
		t.Fatal("unauthorized mutation")
	}
	for _, bad := range []string{`{}`, strings.Replace(body, "fixture_password_12345", "short", 1), body[:len(body)-1] + `,"extra":true}`} {
		if code, _ := operationCall(s, token, path, bad, "", nil); code != 400 {
			t.Fatal("invalid body", code)
		}
	}
	if code, _ := operationCall(s, token, path+"?password=x", body, "", nil); code != 400 {
		t.Fatal("query accepted", code)
	}
	if calls != 0 {
		t.Fatal("invalid mutation reached game")
	}
	for i := 0; i < 2; i++ {
		code, out := operationCall(s, token, path, body, "", nil)
		if code != 200 {
			t.Fatal("reset", code)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "fixture_password_12345") || strings.Contains(string(raw), `"password"`) {
			t.Fatal("password echoed")
		}
	}
	mode = "conflict"
	if code, _ := operationCall(s, token, path, body, "", nil); code != 409 {
		t.Fatal("conflict", code)
	}
	for _, mode = range []string{"missing", "mismatch"} {
		if code, _ := operationCall(s, token, path, body, "", nil); code != 503 {
			t.Fatal("unconfirmed result", code)
		}
	}
	if code, _ := v1TestCall(t, s, token, "GET", "/api/v1/"+path); code != 404 {
		t.Fatal("reset must not be GET", code)
	}
}
