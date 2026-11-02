package gmtool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	repomysql "dnf90.local/gateway/internal/modules/dnf/repository/mysql"
	driver "github.com/go-sql-driver/mysql"
)

func currencyFixture(t *testing.T, shards int) (*Server, repository.AccountManagementUnitOfWork, func() repository.Group) {
	s, u, open, _ := currencyFixtureDatabases(t, shards)
	return s, u, open
}

func currencyFixtureDatabases(t *testing.T, shards int) (*Server, repository.AccountManagementUnitOfWork, func() repository.Group, []string) {
	t.Helper()
	dsn := os.Getenv("DNFBRIDGE_PLAYER_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable MySQL DSN not configured")
	}
	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx := context.Background()
	names := []string{}
	fixtureID := time.Now().UnixNano()
	for i := 0; i < shards; i++ {
		name := fmt.Sprintf("gm_currency_%d_%d", fixtureID, i)
		if _, err = admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec("DROP DATABASE " + name) })
		names = append(names, name)
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = names[0]
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	plan := repository.DatabasePlan{WriteDatabases: names, ReadDatabases: names}
	options := repomysql.MySQLGroupOptions{DatabasePlan: plan}
	if err = repomysql.EnsureMySQLSchema(ctx, db, repomysql.SchemaOptions{AutoCreate: true, DatabasePlan: plan}); err != nil {
		t.Fatal(err)
	}
	if err = repomysql.CheckManagementSchema(ctx, db, options); err == nil {
		t.Fatal("uninitialized schema accepted")
	}
	for range 2 {
		if err = repomysql.EnsureManagementSchema(ctx, db, options); err != nil {
			t.Fatal(err)
		}
	}
	if err = repomysql.CheckManagementSchema(ctx, db, options); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		"CREATE TABLE web_accounts(id BIGINT PRIMARY KEY,account_id VARCHAR(128) NOT NULL UNIQUE,account VARCHAR(64),created_at DATETIME(6),last_login_at DATETIME(6))",
		"CREATE TABLE web_accounts_player_accounts(account_id VARCHAR(128) PRIMARY KEY,disabled BOOLEAN NOT NULL)",
		"INSERT INTO web_accounts(id,account_id,account) VALUES(1,'wallet:1','钱包一'),(2,'wallet:2','钱包二')",
	} {
		if _, err = db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	open := func() repository.Group {
		g, e := repomysql.NewMySQLGroupFromDB(db, options)
		if e != nil {
			t.Fatal(e)
		}
		return g
	}
	g := open()
	for _, id := range []string{"wallet:1", "wallet:2"} {
		if err = g.Account.Save(ctx, repository.AccountRecord{AccountID: id, Metadata: map[string]string{"account_cera": "1000", "keep": "preserved"}}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{cfg: Config{Table: "web_accounts", Password: "fixture"}, db: db, deps: Deps{Repos: func() (repository.Group, bool) { return open(), true }}, www: http.NotFoundHandler(), sessions: map[string]time.Time{}, failures: map[string][]time.Time{}}
	return s, g.Account.(repository.AccountManagementUnitOfWork), open, names
}

type failingReceipts struct {
	repository.ManagementReceiptStore
}

func (f failingReceipts) Append(context.Context, string, []byte) error {
	return errors.New("injected receipt failure")
}

type failingUOW struct {
	repository.AccountManagementUnitOfWork
}

func (f failingUOW) WithinAccountManagement(ctx context.Context, id string, fn func(repository.AccountRepository, repository.ManagementReceiptStore) error) error {
	return f.AccountManagementUnitOfWork.WithinAccountManagement(ctx, id, func(a repository.AccountRepository, r repository.ManagementReceiptStore) error {
		return fn(a, failingReceipts{r})
	})
}

func TestV1CurrencyMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, u, open := currencyFixture(t, shards)
			ctx := context.Background()
			load := func(id string) repository.AccountRecord {
				a, ok, e := open().Account.Load(ctx, id)
				if e != nil || !ok {
					t.Fatal(ok, e)
				}
				return a
			}
			cmd := adminops.CurrencyCommand{AccountID: "wallet:1", Currency: "cera", Amount: "250", Reason: "补偿", Actor: "测试管理员", Key: "currency-same-key-001", Revision: adminops.CurrencyRevision(load("wallet:1"))}
			results := make(chan adminops.CurrencyReceipt, 8)
			errs := make(chan error, 8)
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() { defer wg.Done(); r, e := adminops.AdjustCurrency(ctx, u, cmd); results <- r; errs <- e }()
			}
			wg.Wait()
			close(results)
			close(errs)
			for e := range errs {
				if e != nil {
					t.Fatal(e)
				}
			}
			for r := range results {
				if r.After != "1250" || r.Before != "1000" {
					t.Fatal(r)
				}
			}
			if a := load("wallet:1"); a.Metadata["account_cera"] != "1250" || a.Metadata["keep"] != "preserved" {
				t.Fatal(a)
			}
			if rows, e := u.AccountManagementLog(ctx, "wallet:1", 0, 100); e != nil || len(rows) != 1 {
				t.Fatal(len(rows), e)
			}
			if r, e := adminops.AdjustCurrency(ctx, open().Account.(repository.AccountManagementUnitOfWork), cmd); e != nil || r.After != "1250" {
				t.Fatal(r, e)
			}
			changed := cmd
			changed.Amount = "251"
			if _, e := adminops.AdjustCurrency(ctx, u, changed); !errors.Is(e, adminops.ErrIdempotency) {
				t.Fatal(e)
			}
			cmd.Revision = adminops.CurrencyRevision(load("wallet:1"))
			cmd.Amount = "100"
			wg = sync.WaitGroup{}
			errs = make(chan error, 2)
			for i := 0; i < 2; i++ {
				copy := cmd
				copy.Key = fmt.Sprintf("currency-race-key-%d", i)
				wg.Add(1)
				go func() { defer wg.Done(); _, e := adminops.AdjustCurrency(ctx, u, copy); errs <- e }()
			}
			wg.Wait()
			close(errs)
			success, conflicts := 0, 0
			for e := range errs {
				if e == nil {
					success++
				} else if errors.Is(e, adminops.ErrConflict) {
					conflicts++
				} else {
					t.Fatal(e)
				}
			}
			if success != 1 || conflicts != 1 {
				t.Fatal(success, conflicts)
			}
			cmd.Revision = adminops.CurrencyRevision(load("wallet:1"))
			cmd.Key = "currency-rollback-key"
			if _, e := adminops.AdjustCurrency(ctx, failingUOW{u}, cmd); e == nil {
				t.Fatal("expected rollback")
			}
			if a := load("wallet:1"); a.Metadata["account_cera"] != "1350" || adminops.CurrencyRevision(a) != cmd.Revision {
				t.Fatal("balance not rolled back", a)
			}
			for _, amount := range []string{"0", "-1351", "9223372036854775807", "-9223372036854775808", "01", "1.0"} {
				bad := cmd
				bad.Amount = amount
				if _, e := adminops.AdjustCurrency(ctx, u, bad); !errors.Is(e, adminops.ErrAmount) {
					t.Fatal(amount, e)
				}
			}
			bad := cmd
			bad.AccountID = "Wallet:1"
			if _, e := adminops.AdjustCurrency(ctx, u, bad); e == nil {
				t.Fatal("case alias accepted")
			}
			token, _ := s.startSession()
			status, body := v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:1/currency")
			if status != 200 || body["data"].(map[string]any)["balance"] != "1350" {
				t.Fatal(status, body)
			}
			status, body = v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:1/currency-adjustments?limit=1")
			if status != 200 {
				t.Fatal(status, body)
			}
			cursor := body["page"].(map[string]any)["nextCursor"].(string)
			if cursor == "" {
				t.Fatal("missing page")
			}
			status, body = v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:1/currency-adjustments?limit=1&cursor="+url.QueryEscape(cursor))
			if status != 200 || len(body["data"].([]any)) != 1 {
				t.Fatal(status, body)
			}
			status, _ = v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:2/currency-adjustments?limit=1&cursor="+url.QueryEscape(cursor))
			if status != 400 {
				t.Fatal("cross-account cursor", status)
			}
			payload, _ := json.Marshal(map[string]string{"currency": "cera", "amount": "-50", "revision": cmd.Revision, "reason": "扣减复核"})
			status, body = operationCall(s, token, "accounts/wallet:1/currency-adjustments", string(payload), "currency-http-key-001", nil)
			if status != 200 || body["data"].(map[string]any)["after"] != "1300" {
				t.Fatal(status, body)
			}
			status, _ = operationCall(s, token, "accounts/wallet:1/currency-adjustments", string(payload), "currency-http-key-002", nil)
			if status != 409 {
				t.Fatal("stale revision", status)
			}
			status, _ = operationCall(s, token, "accounts/wallet:1/currency-adjustments", string(payload), "currency-http-key-003", map[string]string{"X-CSRF-Token": ""})
			if status != 403 {
				t.Fatal("csrf", status)
			}
			for _, raw := range []string{"broken", "-1", "", "9223372036854775808"} {
				a := load("wallet:2")
				a.Metadata["account_cera"] = raw
				if e := open().Account.Save(ctx, a); e != nil {
					t.Fatal(e)
				}
				status, _ = v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:2/currency")
				if status != 503 {
					t.Fatal("corrupt balance read", raw, status)
				}
				bad = cmd
				bad.AccountID = "wallet:2"
				bad.Revision = adminops.CurrencyRevision(a)
				if _, e := adminops.AdjustCurrency(ctx, u, bad); e == nil {
					t.Fatal("corrupt balance overwritten", raw)
				}
			}
		})
	}
}

func TestManagedHandlerIsolation(t *testing.T) {
	s := &Server{cfg: Config{Password: strings.Repeat("a", 32)}, sessions: map[string]time.Time{}, failures: map[string][]time.Time{}}
	h := s.ManagementHandler("")
	for _, path := range []string{"/api/accounts/1/currency", "/api/characters/1/inventory", "/console/index.html"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"`+strings.Repeat("a", 32)+`"}`))
	r.Header.Set("Origin", "https://another.invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
