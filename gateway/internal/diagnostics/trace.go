// Package diagnostics records bounded, value-free management request evidence.
package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type key struct{}
type Logger func(string, ...any)
type Stage struct {
	Count  int     `json:"count"`
	MS     float64 `json:"ms"`
	Errors int     `json:"errors,omitempty"`
}
type Trace struct {
	mu                                   sync.Mutex
	id, correlation, parent, route, code string
	fields                               map[string]any
	stages                               map[string]Stage
	dropped                              int
	err                                  error
}

// Release builds set this through -ldflags -X. Keep a constant initializer so
// runtime initialization cannot overwrite the injected revision.
var BuildVersion = "development"

func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "development"
}
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("diagnostic ID entropy unavailable")
	}
	return hex.EncodeToString(b[:])
}
func validID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && len(id) == 32
}
func New(ctx context.Context) context.Context {
	id := ID()
	return context.WithValue(ctx, key{}, &Trace{id: id, correlation: id, fields: map[string]any{}, stages: map[string]Stage{}})
}
func current(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(key{}).(*Trace)
	return t
}
func Enabled(ctx context.Context) bool { return current(ctx) != nil }
func RequestID(ctx context.Context) string {
	if t := current(ctx); t != nil {
		return t.id
	}
	return ""
}

// TrustPeer is called only after the private service identity has been verified.
func TrustPeer(ctx context.Context, h http.Header) {
	if t := current(ctx); t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		if validID(h.Get("X-Correlation-ID")) && validID(h.Get("X-Parent-Request-ID")) {
			t.correlation, t.parent = h.Get("X-Correlation-ID"), h.Get("X-Parent-Request-ID")
		}
		for field, header := range map[string]string{"job_id": "X-GM-Job-ID", "cycle_id": "X-GM-Cycle-ID"} {
			if id := h.Get(header); validID(id) {
				t.fields[field] = id
			}
		}
	}
}
func Headers(ctx context.Context, h http.Header) {
	if t := current(ctx); t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		h.Set("X-Correlation-ID", t.correlation)
		h.Set("X-Parent-Request-ID", t.id)
		for field, header := range map[string]string{"job_id": "X-GM-Job-ID", "cycle_id": "X-GM-Cycle-ID"} {
			if id, ok := t.fields[field].(string); ok && validID(id) {
				h.Set(header, id)
			}
		}
	}
}
func ResponseHeaders(ctx context.Context, h http.Header) {
	if t := current(ctx); t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		h.Set("X-Request-ID", t.id)
		h.Set("X-Correlation-ID", t.correlation)
	}
}

// Field names and string values must come from program constants or opaque IDs,
// never request bodies, search terms, SQL, cookies or credentials.
func Field(ctx context.Context, name string, value any) {
	if s, ok := value.(string); ok {
		if len(s) > 128 {
			value = "invalid_identifier"
		} else {
			for _, c := range s {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ':' || c == '_' || c == '-') {
					value = "invalid_identifier"
					break
				}
			}
		}
	}
	if t := current(ctx); t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		if len(t.fields) < 24 {
			t.fields[name] = value
		}
	}
}
func Route(ctx context.Context, pattern string) {
	if t := current(ctx); t != nil {
		t.mu.Lock()
		t.route = pattern
		t.mu.Unlock()
	}
}
func Fail(ctx context.Context, err error) {
	if err != nil {
		if t := current(ctx); t != nil {
			t.mu.Lock()
			t.err = err
			t.mu.Unlock()
		}
	}
}
func Code(ctx context.Context, code string) {
	if len(code) > 64 {
		code = "invalid_error_code"
	}
	for _, c := range code {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			code = "invalid_error_code"
			break
		}
	}
	if t := current(ctx); t != nil {
		t.mu.Lock()
		t.code = code
		t.mu.Unlock()
	}
}
func Start(ctx context.Context, name string) func(error) {
	t := current(ctx)
	if t == nil {
		return func(error) {}
	}
	start := time.Now()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			s, exists := t.stages[name]
			if !exists && len(t.stages) >= 32 {
				t.dropped++
				return
			}
			s.Count++
			s.MS += float64(time.Since(start).Microseconds()) / 1000
			if err != nil {
				s.Errors++
				t.err = err
			}
			t.stages[name] = s
		})
	}
}
func Fields(ctx context.Context) []any {
	t := current(ctx)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	stages, _ := json.Marshal(t.stages)
	out := []any{"request_id", t.id, "correlation_id", t.correlation, "parent_request_id", t.parent, "route", t.route, "error_code", t.code, "stages", string(stages), "stages_dropped", t.dropped}
	for k, v := range t.fields {
		out = append(out, k, v)
	}
	return append(out, ErrorFields(t.err)...)
}
func Log(log Logger, ctx context.Context, event string, err error, fields ...any) {
	if log == nil {
		return
	}
	Fail(ctx, err)
	base := Fields(ctx)
	if !Enabled(ctx) {
		base = ErrorFields(err)
	}
	log(event, append(base, fields...)...)
}

// SafeRoute retains only known API words; all identifiers and unknown paths are
// redacted. The game-side mux replaces it with its exact registered pattern.
func SafeRoute(path string) string {
	known := " api v1 internal gateway accounts characters benefits recipients redelivery scan scan-v2 tasks run schedule pause inventory containers currency currency-adjustments gold gold-operations skill-points skill-point-operations level level-operations profession profession-operations expert-job expert-job-operations vault vault-operations mailbox mailbox-operations mailbox-cleanups items quests quest-operations activities capabilities presence settings pvf preview state options grant-options pvf-preview pvf-apply online-preview mail sessions login logout "
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 12 {
		return "/unmatched"
	}
	for i, part := range parts {
		if part == "" || !strings.Contains(known, " "+part+" ") {
			parts[i] = "{id}"
		}
	}
	return "/" + strings.Join(parts, "/")
}

type writer struct {
	http.ResponseWriter
	status, bytes int
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *writer) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *writer) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, e
}
func envInt(name string, fallback, min, max int) int {
	n, e := strconv.Atoi(os.Getenv(name))
	if e != nil || n < min || n > max {
		return fallback
	}
	return n
}

// Errors and slow requests are always emitted. Fast successes default to 1/100.
func Wrap(service string, log Logger, next http.Handler) http.Handler {
	version := BuildVersion
	if version == "development" {
		version = buildVersion()
	}
	slow := time.Duration(envInt("PURPLEMAGIC_LOG_SLOW_MS", 1000, 1, 60000)) * time.Millisecond
	every := uint64(envInt("PURPLEMAGIC_LOG_SUCCESS_EVERY", 100, 1, 100000))
	var sequence atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Enabled(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		r = r.WithContext(New(r.Context()))
		Route(r.Context(), SafeRoute(r.URL.Path))
		ResponseHeaders(r.Context(), w.Header())
		started := time.Now()
		out := &writer{ResponseWriter: w}
		defer func() {
			panicked := recover()
			if panicked != nil {
				Code(r.Context(), "panic")
				out.status = 500
			}
			elapsed := time.Since(started)
			Fail(r.Context(), r.Context().Err())
			if out.status == 0 {
				out.status = 200
			}
			t := current(r.Context())
			t.mu.Lock()
			failed := t.err != nil
			t.mu.Unlock()
			if out.status >= 400 || failed || elapsed >= slow || sequence.Add(1)%every == 0 {
				Log(log, r.Context(), "gm-request-complete", nil, "service", service, "version", version, "method", safeMethod(r.Method), "status", out.status, "duration_ms", float64(elapsed.Microseconds())/1000, "bytes", out.bytes, "success_sample_every", every)
			}
			if panicked != nil {
				panic(panicked)
			}
		}()
		next.ServeHTTP(out, r)
	})
}
func safeMethod(method string) string {
	switch method {
	case "GET", "POST", "HEAD", "OPTIONS", "PUT", "DELETE", "PATCH":
		return method
	}
	return "OTHER"
}
func Fingerprint(value string) string { // correlation for opaque cursors, never their contents
	if value == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256Sum(value))
}
