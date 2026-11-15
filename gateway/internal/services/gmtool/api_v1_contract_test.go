package gmtool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1ItemsPaginationAndEmbeddedConsole(t *testing.T) {
	www, err := newStaticHandler("wwwroot")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{www: www, sessions: map[string]time.Time{}, cfg: Config{Password: "fixture"}}
	s.items = &itemIndex{ready: true}
	for i := 1; i <= 45; i++ {
		s.items.entries = append(s.items.entries, itemEntry{ID: uint32(i), Name: "fixture", Kind: "stackable"})
	}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/items?limit=20&kind=stackable"
	count := 0
	firstCursor := ""
	for {
		status, body := v1TestCall(t, s, token, "GET", path)
		if status != 200 {
			t.Fatal(status, body)
		}
		data := body["data"].(map[string]any)
		if data["total"] != float64(45) {
			t.Fatal(data)
		}
		for _, raw := range data["items"].([]any) {
			if _, ok := raw.(map[string]any)["itemId"].(string); !ok {
				t.Fatal("numeric item ID")
			}
			count++
		}
		cursor, _ := body["page"].(map[string]any)["nextCursor"].(string)
		if cursor == "" {
			break
		}
		if firstCursor == "" {
			firstCursor = cursor
		}
		path = "/api/v1/items?limit=20&kind=stackable&cursor=" + url.QueryEscape(cursor)
	}
	if count != 45 {
		t.Fatal("lost items", count)
	}
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/items?limit=20&kind=equipment&cursor="+url.QueryEscape(firstCursor)); status != 400 {
		t.Fatal("changed kind accepted")
	}
	for _, path := range []string{"/console/", "/console/live.js", "/console/ui.js", "/console/preview.html", "/console/vendor/theme-studio.js"} {
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal("missing embedded asset", path, w.Code)
		}
		if path == "/console/" && (!strings.Contains(w.Body.String(), "live.js") || strings.Contains(w.Body.String(), "demo-data.js")) {
			t.Fatal("live page uses preview data")
		}
	}
}

type v1WaitingReader struct {
	repository.CharacterRepository
	deadline time.Time
}

func (r *v1WaitingReader) SummariesForAccounts(ctx context.Context, _ []string) ([]repository.CharacterSummary, error) {
	return r.SearchSummaries(ctx, "", "", 1)
}
func (r *v1WaitingReader) FindSummary(ctx context.Context, _ string) (repository.CharacterSummary, bool, error) {
	_, err := r.SearchSummaries(ctx, "", "", 1)
	return repository.CharacterSummary{}, false, err
}
func (r *v1WaitingReader) SearchSummaries(ctx context.Context, _, _ string, _ int) ([]repository.CharacterSummary, error) {
	r.deadline, _ = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// If the server applied the 15s default read deadline, verify it immediately
	// without making unit tests block for 15 wall-clock seconds.
	if time.Until(r.deadline) > 5*time.Second {
		return nil, context.DeadlineExceeded
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestV1ReadDeadlineAndCancellation(t *testing.T) {
	reader := &v1WaitingReader{}
	s := &Server{www: http.NotFoundHandler(), sessions: map[string]time.Time{}, deps: Deps{Repos: func() (repository.Group, bool) { return repository.Group{Character: reader}, true }}}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/characters", nil)
	request.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	start := time.Now()
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, request)
	budget := reader.deadline.Sub(start)
	if w.Code != 503 || reader.deadline.IsZero() || budget < 14*time.Second || budget > 15500*time.Millisecond || time.Since(start) > time.Second {
		t.Fatal("read deadline not enforced", w.Code, budget, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	w = httptest.NewRecorder()
	s.routes().ServeHTTP(w, request.WithContext(ctx))
	if w.Code != statusClientClosedRequest || time.Since(start) > time.Second {
		t.Fatal("caller cancellation not preserved quietly", w.Code)
	}
}
