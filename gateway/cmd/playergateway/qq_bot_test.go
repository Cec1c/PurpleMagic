package main

import (
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/qqbinding"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQQBotPrivateScope(t *testing.T) {
	gameToken, botToken := strings.Repeat("g", 32), strings.Repeat("b", 32)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+gameToken {
			t.Error("wrong runtime credential")
		}
		var in adminapi.Request
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("bad payload")
		}
		if in.Action == "qq_complete" && (in.QQComplete.Account != "" || in.QQComplete.Code != "012345") {
			t.Error("code-only request changed")
		}
		if in.Action != "qq_settings" && in.Action != "qq_complete" {
			t.Error("wrong action", in.Action)
		}
		json.NewEncoder(w).Encode(adminapi.Response{Version: 1, OK: true, QQSettings: &qqbinding.Settings{Groups: []string{"123456789"}}, QQBinding: &qqbinding.Binding{Account: "private-player", AccountID: "private-id", QQ: "123456789"}})
	}))
	defer upstream.Close()
	client, err := adminapi.NewClient(upstream.URL+adminapi.Path, gameToken)
	if err != nil {
		t.Fatal(err)
	}
	h := withQQBot(http.NotFoundHandler(), client, botToken)
	for _, tc := range []struct {
		path, method, token, body string
		status                    int
	}{
		{"/api/bot/qq-binding/settings", "GET", botToken, "", 200},
		{"/api/bot/qq-binding/settings", "GET", gameToken, "", 404},
		{"/api/bot/qq-binding/complete", "POST", botToken, `{"code":"012345","qq":"123456789","group":"234567890","revision":1}`, 200},
		{"/api/bot/qq-binding/complete", "POST", botToken, `{"action":"change_ban"}`, 400},
		{"/api/bot/qq-binding/complete", "GET", botToken, "", 404},
		{"/api/bot/qq-binding/settings?x=1", "GET", botToken, "", 404},
		{"/api/game/admin", "POST", botToken, "{}", 404},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		h.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "private-player") || strings.Contains(w.Body.String(), "private-id") {
			t.Fatal("bot response exposed account")
		}
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	if calls != 2 {
		t.Fatal("private rejected requests reached game", calls)
	}
	public := handler(config{Upstream: upstream.URL, ProxySecret: gameToken})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/bot/qq-binding/settings", nil)
	r.Header.Set("Authorization", "Bearer "+botToken)
	public.ServeHTTP(w, r)
	if w.Code != 404 || calls != 2 {
		t.Fatal("bot endpoint exposed on player listener")
	}
}

func TestQQBotMembershipRoutesCannotManageBindings(t *testing.T) {
	token := strings.Repeat("b", 32)
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in adminapi.Request
		json.NewDecoder(r.Body).Decode(&in)
		actions = append(actions, in.Action)
		json.NewEncoder(w).Encode(adminapi.Response{Version: 1, OK: true, QQChanged: true, QQChecks: []qqbinding.Check{}})
	}))
	defer server.Close()
	client, _ := adminapi.NewClient(server.URL+adminapi.Path, strings.Repeat("g", 32))
	h := withQQBot(http.NotFoundHandler(), client, token)
	for _, tc := range []struct {
		path, body string
		status     int
	}{{"checks", "{}", 200}, {"check-due", `{"qq":"234567890"}`, 200}, {"check-result", `{"accountId":"fixture:main","qq":"234567890","revision":2,"lease":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","settingsRevision":4,"outcome":"absent","group":""}`, 200}, {"check-result", `{"action":"delete"}`, 400}, {"change", `{"action":"delete"}`, 404}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/bot/qq-binding/"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if strings.Join(actions, ",") != "qq_checks,qq_check_due,qq_check_result" {
		t.Fatal(actions)
	}
}
