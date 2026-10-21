package servicehttp

import (
	"context"
	"dnf90.local/gateway/internal/diagnostics"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPeerTraceAndErrorClasses(t *testing.T) {
	t.Setenv("PURPLEMAGIC_LOG_SUCCESS_EVERY", "1")
	token, hash := strings.Repeat("t", 48), strings.Repeat("a", 64)
	var events []map[string]any
	logger := func(_ string, args ...any) {
		m := map[string]any{}
		for i := 0; i+1 < len(args); i += 2 {
			m[args[i].(string)] = args[i+1]
		}
		events = append(events, m)
	}
	server := httptest.NewServer(diagnostics.Wrap("peer", logger, Protect(token, hash, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/business":
			w.WriteHeader(409)
			w.Write([]byte(`{"error":{"code":"revision_conflict","message":"secret"}}`))
		case "/invalid":
			w.Write([]byte(`secret invalid JSON`))
		case "/generation":
			w.Header().Set("X-PVF-SHA256", strings.Repeat("b", 64))
			w.Write([]byte(`{}`))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
		default:
			w.Write([]byte(`{}`))
		}
	}))))
	defer server.Close()
	c, e := New(Config{URL: server.URL, Token: token, PVFSHA256: hash})
	if e != nil {
		t.Fatal(e)
	}
	ctx := diagnostics.New(context.Background())
	var out any
	if e = c.JSON(ctx, "/ok", nil, &out); e != nil {
		t.Fatal(e)
	}
	if len(events) != 1 || events[0]["parent_request_id"] != diagnostics.RequestID(ctx) || events[0]["correlation_id"] != diagnostics.RequestID(ctx) || events[0]["request_id"] == diagnostics.RequestID(ctx) {
		t.Fatal(events)
	}
	for path, kind := range map[string]string{"/business": "http_status", "/invalid": "invalid_json", "/generation": "generation_mismatch"} {
		e = c.JSON(ctx, path, nil, &out)
		var peer *PeerError
		if !errors.As(e, &peer) || peer.Kind != kind {
			t.Fatal(path, e)
		}
		if path == "/business" && (peer.Status != 409 || peer.Code != "revision_conflict" || peer.RequestID == "") {
			t.Fatal(peer)
		}
		if strings.Contains(e.Error(), "secret") {
			t.Fatal(e)
		}
	}
	run, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	if e = c.JSON(run, "/slow", nil, &out); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("lost timeout cause", e)
	}
	// Only an authenticated private request may supply correlation headers.
	r := httptest.NewRequest("POST", "/ok", nil)
	r.Header.Set("X-Correlation-ID", diagnostics.RequestID(ctx))
	r.Header.Set("X-Parent-Request-ID", diagnostics.RequestID(ctx))
	w := httptest.NewRecorder()
	diagnostics.Wrap("test", nil, Protect(token, hash, http.NotFoundHandler())).ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("X-Correlation-ID") == diagnostics.RequestID(ctx) {
		t.Fatal(w.Code, w.Header())
	}
}
func TestPeerConnectionRefusedCauseRetained(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	s.Close()
	c, _ := New(Config{URL: s.URL, Token: strings.Repeat("t", 48), PVFSHA256: strings.Repeat("a", 64)})
	var out any
	e := c.JSON(context.Background(), "/secret?token=secret", nil, &out)
	raw, _ := json.Marshal(diagnostics.ErrorFields(e))
	if e == nil || !strings.Contains(string(raw), "network") || strings.Contains(string(raw), "secret") {
		t.Fatal(e, string(raw))
	}
}
