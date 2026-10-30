package main

import (
	"crypto/subtle"
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/modules/dnf/qqbinding"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Only the private listener mounts this adapter. Its separate credential allows
// reading binding policy and consuming a player-owned code, never GM operations.
func withQQBot(next http.Handler, client *adminapi.Client, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/bot/qq-binding/") {
			next.ServeHTTP(w, r)
			return
		}
		if len(token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		in := adminapi.Request{}
		if r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/bot/qq-binding/jobs":
			in.Action = "qq_jobs"
		case r.Method == "POST" && r.URL.Path == "/api/bot/qq-binding/ack":
			var ack moderation.QQAck
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&ack) != nil || d.Decode(new(any)) != io.EOF {
				http.Error(w, "invalid request", 400)
				return
			}
			in.Action = "qq_ack"
			in.QQAck = &ack
		case r.Method == "POST" && r.URL.Path == "/api/bot/qq-binding/blocked":
			var q struct {
				QQ string `json:"qq"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF || !qqbinding.Number.MatchString(q.QQ) {
				http.Error(w, "invalid request", 400)
				return
			}
			in.Action = "qq_blocked"
			in.QQ = q.QQ
		case r.Method == "GET" && r.URL.Path == "/api/bot/qq-binding/settings":
			in.Action = "qq_settings"
		case r.Method == "POST" && r.URL.Path == "/api/bot/qq-binding/complete":
			var c qqbinding.Complete
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
				http.Error(w, "invalid request", 400)
				return
			}
			in.Action = "qq_complete"
			in.QQComplete = &c
		default:
			http.NotFound(w, r)
			return
		}
		out, err := client.Call(r.Context(), in)
		if err != nil {
			if out.Error == "" {
				out.Error = "绑定服务暂不可用，请稍后重试"
			}
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": out.Error})
			return
		}
		var data any = out.QQSettings
		if in.Action == "qq_complete" {
			data = map[string]bool{"bound": true}
		}
		switch in.Action {
		case "qq_jobs":
			data = out.QQActions
		case "qq_ack":
			data = map[string]bool{"saved": true}
		case "qq_blocked":
			data = map[string]bool{"blocked": out.QQBlocked}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
	})
}
