package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	"dnf90.local/gateway/internal/servicehttp"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestV1ExternalDiagnosticCorrelation(t *testing.T) {
	t.Setenv("PURPLEMAGIC_LOG_SUCCESS_EVERY", "1")
	var outer, inner map[string]any
	capture := func(target *map[string]any) func(string, ...any) {
		return func(event string, args ...any) {
			if event != "gm-request-complete" {
				return
			}
			m := map[string]any{}
			for i := 0; i+1 < len(args); i += 2 {
				m[args[i].(string)] = args[i+1]
			}
			*target = m
		}
	}
	game := &Server{cfg: Config{Logger: capture(&inner)}}
	peerServer := httptest.NewServer(game.GatewayHandler(strings.Repeat("t", 48), strings.Repeat("a", 64)))
	defer peerServer.Close()
	peer, e := servicehttp.New(servicehttp.Config{URL: peerServer.URL, Token: strings.Repeat("t", 48), PVFSHA256: strings.Repeat("a", 64)})
	if e != nil {
		t.Fatal(e)
	}
	external := &Server{cfg: Config{Logger: capture(&outer)}, sessions: map[string]time.Time{"test-session": time.Now().Add(time.Hour)}, www: http.NotFoundHandler()}
	r := httptest.NewRequest("GET", "/api/v1/unknown-secret?password=secret", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "test-session"})
	r.Header.Set("X-Correlation-ID", strings.Repeat("f", 32))
	w := httptest.NewRecorder()
	external.ExternalHandler("", "", peer).ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if outer["correlation_id"] != inner["correlation_id"] || inner["parent_request_id"] != outer["request_id"] || outer["peer_request_id"] != inner["request_id"] || w.Header().Get("X-Request-ID") != inner["request_id"] || w.Header().Get("X-Correlation-ID") != outer["correlation_id"] || outer["correlation_id"] == strings.Repeat("f", 32) {
		t.Fatal(outer, inner, w.Header())
	}
	var envelope v1Envelope
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.RequestID != w.Header().Get("X-Request-ID") {
		t.Fatal(w.Body.String())
	}
	if outer["error_code"] != "not_found" || inner["error_code"] != "not_found" {
		t.Fatal(outer, inner)
	}
	raw, _ := json.Marshal([]any{outer, inner})
	if strings.Contains(string(raw), "secret") {
		t.Fatal(string(raw))
	}
}

func diagnosticStages(t *testing.T, ctx context.Context) map[string]diagnostics.Stage {
	t.Helper()
	fields := diagnostics.Fields(ctx)
	for i := 0; i < len(fields); i += 2 {
		if fields[i] == "stages" {
			var out map[string]diagnostics.Stage
			if e := json.Unmarshal([]byte(fields[i+1].(string)), &out); e != nil {
				t.Fatal(e)
			}
			return out
		}
	}
	t.Fatal("missing stages")
	return nil
}
func TestV1DiagnosticsMySQL(t *testing.T) {
	s, _, open := currencyFixture(t, 1)
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	t.Run("connection_wait", func(t *testing.T) {
		s.db.SetMaxOpenConns(1)
		defer s.db.SetMaxOpenConns(0)
		conn, e := s.db.Conn(context.Background())
		check(e)
		defer conn.Close()
		ctx, cancel := context.WithTimeout(diagnostics.New(context.Background()), 120*time.Millisecond)
		defer cancel()
		_, e = diagnostics.Query(ctx, s.db, "probe", "SELECT 1")
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal(e)
		}
		stages := diagnosticStages(t, ctx)
		if stages["probe.connection"].MS < 80 || stages["probe.connection"].Errors != 1 || stages["probe.query_rows"].Count != 0 {
			t.Fatal(stages)
		}
	})
	t.Run("slow_query", func(t *testing.T) {
		ctx := diagnostics.New(context.Background())
		rows, e := diagnostics.Query(ctx, s.db, "probe", "SELECT SLEEP(0.15)")
		check(e)
		for rows.Next() {
			var n int
			check(rows.Scan(&n))
		}
		check(rows.Close())
		stages := diagnosticStages(t, ctx)
		if stages["probe.query_rows"].MS < 100 || stages["probe.connection"].Count != 1 {
			t.Fatal(stages)
		}
		// The explicit connection returns to the pool after both EOF and Close.
		if s.db.Stats().InUse != 0 {
			t.Fatal(s.db.Stats())
		}
	})
	g := open()
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: "wallet:1", CharacterID: "100", Name: "fixture-secret", CreatedAt: past}))
	store, e := testbenefits.NewStore(s.db, "web_accounts")
	check(e)
	check(store.EnsureSchema(ctx))
	s.deps.Benefits = store
	rule, e := store.CreateCampaign(ctx, testbenefits.Job{Gold: 7, Title: "fixture"}, "", true)
	check(e)
	t.Run("delivery_lock_wait_and_receipt", func(t *testing.T) {
		tx, e := s.db.BeginTx(ctx, nil)
		check(e)
		defer tx.Rollback()
		_, e = tx.ExecContext(ctx, "UPDATE dnf_accounts SET honor_exp=honor_exp WHERE account_id='wallet:1'")
		check(e)
		run, cancel := context.WithTimeout(diagnostics.New(ctx), 300*time.Millisecond)
		defer cancel()
		job := rule.Reward
		job.ID = rule.ID
		job.CampaignID = rule.ID
		rec := testbenefits.Recipient{AccountID: "wallet:1", CharacterID: "100", CreatedAt: past}
		_, e = testbenefits.DeliverWithReceipt(run, g, job, rec)
		if e == nil {
			t.Fatal("blocked write succeeded")
		}
		stages := diagnosticStages(t, run)
		if stages["delivery.account_locked_read"].MS < 150 || stages["delivery.account_locked_read"].Errors != 1 || stages["delivery.commit"].Count != 0 {
			t.Fatal(stages, e)
		}
		check(tx.Rollback())
		run = diagnostics.New(ctx)
		receipt, e := testbenefits.DeliverWithReceipt(run, g, job, rec)
		check(e)
		if receipt.MailID == "" {
			t.Fatal("missing receipt")
		}
		stages = diagnosticStages(t, run)
		for _, name := range []string{"delivery.connection", "delivery.commit", "delivery.mail_append", "delivery.receipt_write"} {
			if stages[name].Count != 1 {
				t.Fatal(name, stages)
			}
		}
		second, e := testbenefits.DeliverWithReceipt(diagnostics.New(ctx), g, job, rec)
		check(e)
		if second.MailID != receipt.MailID {
			t.Fatal("duplicate reward", receipt, second)
		}
	})
	t.Run("details_and_account_request", func(t *testing.T) {
		for _, path := range []string{"/api/v1/accounts", "/api/v1/benefits/" + rule.ID + "/recipients"} {
			t.Setenv("PURPLEMAGIC_LOG_SUCCESS_EVERY", "1")
			var fields []any
			s.cfg.Logger = func(event string, args ...any) {
				if event == "gm-request-complete" {
					fields = args
				}
			}
			s.sessions["fixture-session"] = time.Now().Add(time.Hour)
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.AddCookie(&http.Cookie{Name: "dfo_gm_session", Value: "fixture-session"})
			w := httptest.NewRecorder()
			s.v1Routes().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(path, w.Code, w.Body.String())
			}
			raw, _ := json.Marshal(fields)
			if !strings.Contains(string(raw), ".connection") || !strings.Contains(string(raw), "response.serialize_write") || strings.Contains(string(raw), "fixture-secret") {
				t.Fatal(string(raw))
			}
		}
	})
}
