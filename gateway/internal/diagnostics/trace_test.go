package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	mysql "github.com/go-sql-driver/mysql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func asMap(fields []any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		out[fields[i].(string)] = fields[i+1]
	}
	return out
}
func TestCompletionRedactionSamplingAndCancellation(t *testing.T) {
	t.Setenv("PURPLEMAGIC_LOG_SUCCESS_EVERY", "3")
	t.Setenv("PURPLEMAGIC_LOG_SLOW_MS", "20")
	var events []map[string]any
	log := func(_ string, args ...any) { events = append(events, asMap(args)) }
	h := Wrap("test", log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "error":
			Fail(r.Context(), &url.Error{Op: "GET", URL: "https://secret:password@private/?token=secret", Err: context.DeadlineExceeded})
			Code(r.Context(), "unavailable")
			w.WriteHeader(503)
		case "slow":
			time.Sleep(30 * time.Millisecond)
		}
	}))
	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/accounts/secret?token=secret", nil))
	}
	if len(events) != 1 {
		t.Fatal("sampling", len(events))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/accounts/secret?mode=error", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/accounts/secret?mode=slow", nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/accounts", nil).WithContext(ctx))
	if len(events) != 4 || events[1]["error_kind"] != "timeout" || events[3]["error_kind"] != "canceled" {
		t.Fatal(events)
	}
	for _, e := range events {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "password") {
			t.Fatal("sensitive data logged", string(raw))
		}
	}
}
func TestTraceBoundedConcurrentStagesAndTrustedParent(t *testing.T) {
	ctx := New(context.Background())
	id := RequestID(ctx)
	h := http.Header{"X-Correlation-Id": {ID()}, "X-Parent-Request-Id": {ID()}}
	TrustPeer(ctx, h)
	fields := asMap(Fields(ctx))
	if fields["parent_request_id"] != h.Get("X-Parent-Request-ID") || fields["request_id"] != id {
		t.Fatal(fields)
	}
	TrustPeer(ctx, http.Header{"X-Correlation-Id": {"forged\nsecret"}})
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			done := Start(ctx, fmt.Sprint("stage", i))
			done(nil)
			done(errors.New("ignored"))
		}(i)
	}
	wg.Wait()
	fields = asMap(Fields(ctx))
	var stages map[string]Stage
	json.Unmarshal([]byte(fields["stages"].(string)), &stages)
	if len(stages) != 32 || fields["stages_dropped"] != 48 {
		t.Fatal(fields)
	}
}
func TestDatabaseErrorDoesNotExposeSQLOrValues(t *testing.T) {
	raw, _ := json.Marshal(asMap(ErrorFields(&mysql.MySQLError{Number: 1062, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: "SQL password secret"})))
	if strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), "1062") {
		t.Fatal(string(raw))
	}
}
