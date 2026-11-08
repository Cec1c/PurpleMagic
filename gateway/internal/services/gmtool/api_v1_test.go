package gmtool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	repomysql "dnf90.local/gateway/internal/modules/dnf/repository/mysql"
	driver "github.com/go-sql-driver/mysql"
)

func v1TestCall(t *testing.T, s *Server, token, method, path string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if out["requestId"] == "" || out["requestId"] != w.Header().Get("X-Request-ID") || out["observedAt"] == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing v1 envelope", out)
	}
	return w.Code, out
}
func TestV1AuthenticationValidationAndUnavailable(t *testing.T) {
	s := &Server{www: http.NotFoundHandler(), sessions: map[string]time.Time{}, cfg: Config{Password: "fixture"}}
	if status, out := v1TestCall(t, s, "", "GET", "/api/v1/accounts"); status != 401 || out["error"].(map[string]any)["code"] != "authentication_required" {
		t.Fatal(status, out)
	}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/accounts?limit=0", "/api/v1/accounts?limit=101", "/api/v1/accounts?limit=1&limit=2", "/api/v1/accounts?unknown=1", "/api/v1/accounts?q=" + strings.Repeat("a", 129), "/api/v1/accounts?state=online", "/api/v1/items?kind=bad", "/api/v1/characters?cursor=bad"} {
		if status, out := v1TestCall(t, s, token, "GET", path); status != 400 {
			t.Fatal(path, status, out)
		}
	}
	for _, check := range []struct {
		method, path string
		status       int
	}{{"POST", "/api/v1/accounts", 405}, {"GET", "/api/v1/missing", 404}, {"GET", "/api/v1/accounts", 503}, {"GET", "/api/v1/items", 503}, {"GET", "/api/v1/capabilities", 200}} {
		if status, out := v1TestCall(t, s, token, check.method, check.path); status != check.status {
			t.Fatal(check, status, out)
		}
	}
	good := s.v1CursorToken("accounts", "filter", "9007199254740993")
	if after, err := s.v1CursorAfter(good, "accounts", "filter"); err != nil || after != "9007199254740993" {
		t.Fatal(after, err)
	}
	for _, bad := range []struct{ token, resource, filter string }{{good, "characters", "filter"}, {good, "accounts", "changed"}, {"x" + good, "accounts", "filter"}} {
		if _, err := s.v1CursorAfter(bad.token, bad.resource, bad.filter); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
}

type v1CountingDB struct {
	repomysql.SQLDBAdapter
	queries []string
}

func (d *v1CountingDB) QueryContext(ctx context.Context, q string, args ...any) (repomysql.SQLRows, error) {
	d.queries = append(d.queries, q)
	return d.SQLDBAdapter.QueryContext(ctx, q, args...)
}
func (d *v1CountingDB) QueryRowContext(ctx context.Context, q string, args ...any) repomysql.SQLRow {
	d.queries = append(d.queries, q)
	return d.SQLDBAdapter.QueryRowContext(ctx, q, args...)
}

func TestV1DirectoryMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d-shards", shards), func(t *testing.T) { testV1DirectoryMySQL(t, shards) })
	}
}

func testV1DirectoryMySQL(t *testing.T, shards int) {
	dsn := os.Getenv("DNFBRIDGE_PLAYER_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable MySQL DSN not configured")
	}
	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	name := fmt.Sprintf("gm_v1_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE " + name)
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	databases := []string{name}
	for i := 1; i < shards; i++ {
		shard := fmt.Sprintf("%s_shard%d", name, i)
		if _, err := admin.Exec("CREATE DATABASE " + shard + " CHARACTER SET utf8mb4"); err != nil {
			t.Fatal(err)
		}
		defer admin.Exec("DROP DATABASE " + shard)
		databases = append(databases, shard)
	}
	plan := repository.DatabasePlan{ReadDatabases: databases, WriteDatabases: databases}
	if err := repomysql.EnsureMySQLSchema(ctx, db, repomysql.SchemaOptions{AutoCreate: true, DatabasePlan: plan}); err != nil {
		t.Fatal(err)
	}
	counted := &v1CountingDB{SQLDBAdapter: repomysql.SQLDBAdapter{DB: db}}
	g, err := repomysql.NewMySQLGroup(counted, repomysql.MySQLGroupOptions{DatabasePlan: plan})
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		"CREATE TABLE web_accounts (id BIGINT PRIMARY KEY,account_id VARCHAR(128) NOT NULL UNIQUE,account VARCHAR(64) NOT NULL,created_at DATETIME(6),last_login_at DATETIME(6)) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		"CREATE TABLE web_accounts_player_accounts (account_id VARCHAR(128) PRIMARY KEY,disabled BOOLEAN NOT NULL) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci",
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 27; i++ {
		accountID := fmt.Sprintf("dnf:%d", i)
		if err := g.Account.Save(ctx, repository.AccountRecord{AccountID: accountID}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES (?,?,?)", i, accountID, fmt.Sprintf("player%02d", i)); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			if _, err := db.Exec("INSERT INTO web_accounts_player_accounts VALUES (?,true)", accountID); err != nil {
				t.Fatal(err)
			}
		}
		for j := 0; j < 2; j++ {
			c := repository.CharacterRecord{CharacterID: fmt.Sprintf("%04d", i*10+j), AccountID: accountID, Slot: j, Name: fmt.Sprintf("探险%d-%d", i, j), Level: 90, Job: "0", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			if err := g.Character.Save(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES (9007199254740993,'dnf:large','literal_%!')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO web_accounts_player_accounts VALUES ('DNF:large',true)"); err != nil {
		t.Fatal(err)
	}
	if err := g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "9998", AccountID: "dnf:large", Name: "literal_%!", Slot: 0}); err != nil {
		t.Fatal(err)
	}
	if err := g.Account.Save(ctx, repository.AccountRecord{AccountID: "dnf:large"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "9999", AccountID: "dnf:large", Name: "deleted", Slot: 1, Stats: map[string]int64{"delete_flag": 1}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{Table: "web_accounts", Password: "fixture"}, db: db, deps: Deps{Repos: func() (repository.Group, bool) { return g, true }}, www: http.NotFoundHandler(), sessions: map[string]time.Time{}, failures: map[string][]time.Time{}}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 20, 100} {
		counted.queries = nil
		status, out := v1TestCall(t, s, token, "GET", fmt.Sprintf("/api/v1/accounts?limit=%d", limit))
		if status != 200 {
			t.Fatal(status, out)
		}
		summaries, wallets := 0, 0
		for _, query := range counted.queries {
			if strings.Contains(query, "dnf_characters") {
				summaries++
			} else if strings.Contains(query, "dnf_account_metadata") && strings.Contains(query, "gm_currency_sequence_v1") && strings.Contains(query, "account_cera") {
				wallets++
			} else {
				t.Fatal("unexpected directory query", query)
			}
			if strings.Contains(query, "_stats") || strings.Contains(query, "_roster") {
				t.Fatal("full character state loaded", query)
			}
		}
		if len(out["data"].([]any)) > limit || summaries != shards || wallets < 1 || wallets > shards {
			t.Fatal("directory queries must scale with shards, not accounts or roles", counted.queries)
		}
	}
	seen := map[string]bool{}
	path := "/api/v1/accounts?limit=7"
	var firstCursor string
	for {
		status, out := v1TestCall(t, s, token, "GET", path)
		if status != 200 {
			t.Fatal(status, out)
		}
		for _, raw := range out["data"].([]any) {
			a := raw.(map[string]any)
			id := a["panelId"].(string)
			if seen[id] {
				t.Fatal("duplicate pagination row", id)
			}
			seen[id] = true
			want := float64(2)
			if id == "9007199254740993" {
				want = 1
				if a["disabled"] != false {
					t.Fatal("different-case policy identity matched", a)
				}
			}
			if a["characterCount"] != want || a["lastLoginAt"] != nil {
				t.Fatal(a)
			}
		}
		cursor, _ := out["page"].(map[string]any)["nextCursor"].(string)
		if cursor == "" {
			break
		}
		if firstCursor == "" {
			firstCursor = cursor
		}
		path = "/api/v1/accounts?limit=7&cursor=" + url.QueryEscape(cursor)
	}
	if len(seen) != 28 || !seen["9007199254740993"] {
		t.Fatal("pagination omitted rows", seen)
	}
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/accounts?limit=7&state=disabled&cursor="+url.QueryEscape(firstCursor)); status != 400 {
		t.Fatal("changed-filter cursor accepted")
	}
	for _, path := range []string{"/api/v1/accounts?q=%25", "/api/v1/characters?q=%25"} {
		status, out := v1TestCall(t, s, token, "GET", path)
		if status != 200 || len(out["data"].([]any)) != 1 {
			t.Fatal("LIKE wildcard not treated literally", path, status, out)
		}
	}
	status, out := v1TestCall(t, s, token, "GET", "/api/v1/accounts?state=disabled&limit=100")
	if status != 200 || len(out["data"].([]any)) != 9 {
		t.Fatal("disabled filter", status, out)
	}
	status, out = v1TestCall(t, s, token, "GET", "/api/v1/accounts/dnf%3Alarge/characters")
	if status != 200 || len(out["data"].([]any)) != 1 {
		t.Fatal("deleted role leaked", status, out)
	}
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/characters/9999"); status != 404 {
		t.Fatal("deleted role visible")
	}
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/accounts/missing"); status != 404 {
		t.Fatal("missing account")
	}
	status, out = v1TestCall(t, s, token, "GET", "/api/v1/characters/0010")
	if status != 200 || out["data"].(map[string]any)["online"] != "unknown" {
		t.Fatal("online state invented", status, out)
	}
	// Merge pages across shards in the same byte order as the cursor contract.
	path = "/api/v1/characters?limit=7"
	last, count := "", 0
	for {
		status, page := v1TestCall(t, s, token, "GET", path)
		if status != 200 {
			t.Fatal(status, page)
		}
		for _, raw := range page["data"].([]any) {
			id := raw.(map[string]any)["characterId"].(string)
			if id <= last {
				t.Fatal("unstable role pagination", last, id)
			}
			last = id
			count++
		}
		cursor, _ := page["page"].(map[string]any)["nextCursor"].(string)
		if cursor == "" {
			break
		}
		path = "/api/v1/characters?limit=7&cursor=" + url.QueryEscape(cursor)
	}
	if count != 55 {
		t.Fatal("missing roles across shards", count)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	reader := g.Character.(repository.CharacterSummaryReader)
	if _, err := reader.SearchSummaries(canceled, "", "", 20); err == nil {
		t.Fatal("canceled SQL returned success")
	}
	// The optional local browser consumes this real HTTP handler and disposable DB.
	testV1OperationsWithGroup(t, s, g, db)
	testV1ResourcesWithGroup(t, s, g, counted)
	if script := os.Getenv("DNF90GM_V1_BROWSER_SCRIPT"); shards == 1 && script != "" {
		s.www, err = newStaticHandler("wwwroot")
		if err != nil {
			t.Fatal(err)
		}
		s.items.entries[0].StackLimit = 1
		server := httptest.NewServer(s.routes())
		defer server.Close()
		command := exec.Command("python", script, "--url", server.URL)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("real backend browser: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
	}
	// A missing summary dependency is an error, never an empty success page.
	if _, err := db.Exec("DROP TABLE dnf_characters"); err != nil {
		t.Fatal(err)
	}
	status, out = v1TestCall(t, s, token, "GET", "/api/v1/accounts")
	if status != 503 || out["data"] != nil || strings.Contains(fmt.Sprint(out), name) {
		t.Fatal("database failure disguised/leaked", status, out)
	}
}
