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
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	driver "github.com/go-sql-driver/mysql"
)

func operationCall(s *Server, token, path, body, key string, headers map[string]string) (int, map[string]any) {
	r := httptest.NewRequest("POST", "/api/v1/"+path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", s.v1CSRF(r))
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func TestV1WriteGuards(t *testing.T) {
	s := &Server{cfg: Config{Password: "fixture"}, www: http.NotFoundHandler(), sessions: map[string]time.Time{}}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		headers map[string]string
		status  int
	}{
		{map[string]string{"X-CSRF-Token": ""}, 403}, {map[string]string{"X-CSRF-Token": "wrong"}, 403},
		{map[string]string{"Sec-Fetch-Site": "cross-site"}, 403}, {map[string]string{"Content-Type": "text/plain"}, 415},
	} {
		status, out := operationCall(s, token, "mail/preview", `{}`, "fixture-request-key", tt.headers)
		if status != tt.status || out["requestId"] == "" {
			t.Fatal(status, out)
		}
	}
	for _, body := range []string{`null`, `{} {}`, `{"unknown":1}`, `{"title":"` + strings.Repeat("x", 4096) + `"}`} {
		status, out := operationCall(s, token, "benefits", body, "fixture-request-key", nil)
		if status != 400 {
			t.Fatal(status, out)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	csrf := s.v1CSRF(r)
	other, _ := s.startSession()
	if status, _ := operationCall(s, other, "mail/preview", `{}`, "fixture-request-key", map[string]string{"X-CSRF-Token": csrf}); status != 403 {
		t.Fatal("cross-session csrf accepted")
	}
	s.endSession(token)
	if status, _ := operationCall(s, token, "mail/preview", `{}`, "fixture-request-key", nil); status != 401 {
		t.Fatal("expired session accepted")
	}
}

// Runs within both single-shard and two-shard disposable MariaDB fixtures.
func testV1OperationsWithGroup(t *testing.T, s *Server, g repository.Group, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	store, err := testbenefits.NewStore(db, "web_accounts")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s.deps.Benefits = store
	s.items = &itemIndex{ready: true, entries: []itemEntry{{ID: 490009735, Name: "Fixture sky box", Kind: "stackable"}, {ID: 1, Name: "Equipment", Kind: "equipment"}}, byID: map[uint32]int{490009735: 0, 1: 1}}
	token, _ := s.startSession()
	post := func(path, body, key string, want int) map[string]any {
		t.Helper()
		status, out := operationCall(s, token, path, body, key, nil)
		if status != want {
			t.Fatalf("%s got %d want %d: %v", path, status, want, out)
		}
		if want == 200 {
			return out["data"].(map[string]any)
		}
		return out
	}
	get := func(path string) map[string]any {
		t.Helper()
		status, out := v1TestCall(t, s, token, "GET", "/api/v1/"+path)
		if status != 200 {
			t.Fatal(path, status, out)
		}
		return out
	}
	create := `{"scope":"account","itemId":"490009735","count":1,"title":"V1 account rule","autoNewCharacters":false}`
	post("benefits", create, "", 400)
	post("benefits", strings.Replace(create, `"account"`, `"wrong"`, 1), "invalid-scope-key", 422)
	post("benefits", strings.Replace(create, `"490009735"`, `"2"`, 1), "invalid-item-key1", 422)
	post("benefits", `{"scope":"character","itemId":"1","count":2,"title":"equipment"}`, "invalid-count-key", 422)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out := operationCall(s, token, "benefits", create, "concurrent-rule-key", nil)
			if code != 200 {
				failures <- fmt.Sprint(code, out)
				return
			}
			ids <- out["data"].(map[string]any)["id"].(string)
		}()
	}
	wg.Wait()
	close(ids)
	close(failures)
	for f := range failures {
		t.Fatal(f)
	}
	id := ""
	for value := range ids {
		if id != "" && id != value {
			t.Fatal("duplicate rule created")
		}
		id = value
	}
	// Reopening the store retains the key and distinguishes content/scope changes.
	s.deps.Benefits, _ = testbenefits.NewStore(db, "web_accounts")
	if post("benefits", create, "concurrent-rule-key", 200)["id"] != id {
		t.Fatal("key lost on restart")
	}
	post("benefits", strings.Replace(create, `"account"`, `"character"`, 1), "concurrent-rule-key", 409)
	c := get("benefits/" + id)["data"].(map[string]any)
	if c["enabled"] != false || c["scope"] != "account" {
		t.Fatal(c)
	}
	base := "benefits/" + id
	j := post(base+"/preview", `{}`, "account-preview-key", 200)
	jobID := j["id"].(string)
	if j["total"] != float64(28) || j["sent"] != float64(0) {
		t.Fatal("account scope target count", j)
	}
	if post(base+"/preview", `{}`, "account-preview-key", 200)["id"] != jobID {
		t.Fatal("preview not idempotent")
	}
	run := func(j map[string]any, retry bool, want int) map[string]any {
		return post("tasks/"+j["id"].(string)+"/run", fmt.Sprintf(`{"confirm":true,"revision":%.0f,"retry":%t}`, j["revision"], retry), "", want)
	}
	run(j, false, 409)
	post(base+"/state", `{"revision":0,"enabled":true}`, "", 422)
	post(base+"/state", `{"revision":0,"enabled":true,"confirm":true}`, "", 200)
	post(base+"/state", `{"revision":0,"enabled":false,"confirm":true}`, "", 409)
	post("tasks/"+jobID+"/run", `{"revision":0,"confirm":false}`, "", 422)
	first := run(j, false, 200)
	if first["sent"] != float64(10) {
		t.Fatal("unbounded batch", first)
	}
	run(j, false, 409)
	j = first
	for j["pending"].(float64) > 0 {
		j = run(j, false, 200)
	}
	if j["sent"] != float64(28) || j["failed"] != float64(0) {
		t.Fatal(j)
	}
	// Claim/removal cannot erase permanent account-scoped receipts.
	m, _, err := g.Mailbox.Load(ctx, "0010")
	if err != nil || len(m.Mails) != 1 {
		t.Fatal("unexpected first mailbox", err, len(m.Mails))
	}
	for key := range m.Mails {
		delete(m.Mails, key)
	}
	if err = g.Mailbox.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	p := post(base+"/preview", `{}`, "after-claim-preview", 200)
	if p["sent"] != float64(28) || p["pending"] != float64(0) {
		t.Fatal("claim erased receipt", p)
	}
	// Character rules independently deliver once to every existing role.
	charRule := post("benefits", strings.ReplaceAll(strings.Replace(create, `"account"`, `"character"`, 1), "V1 account rule", "V1 character rule"), "character-rule-key", 200)
	charBase := "benefits/" + charRule["id"].(string)
	post(charBase+"/state", `{"revision":0,"enabled":true,"confirm":true}`, "", 200)
	chars := post(charBase+"/preview", `{}`, "character-preview-key", 200)
	if chars["total"] != float64(55) {
		t.Fatal("character scope target count", chars)
	}
	for chars["pending"].(float64) > 0 {
		chars = run(chars, false, 200)
	}
	if chars["failed"] != float64(0) {
		t.Fatal(chars)
	}
	// A direct mail preview changes no assets and is content-bound across restarts.
	mail := `{"accountId":"dnf:1","characterId":"0010","title":"V1 single mail","gold":17}`
	post("mail/preview", strings.Replace(mail, "dnf:1", "dnf:2", 1), "wrong-recipient-key", 422)
	single := post("mail/preview", mail, "single-mail-key-01", 200)
	m, _, _ = g.Mailbox.Load(ctx, "0010")
	if len(m.Mails) != 1 {
		t.Fatal("preview sent mail")
	}
	post("mail/preview", strings.Replace(mail, ":17", ":18", 1), "single-mail-key-01", 409)
	sent := run(single, false, 200)
	if sent["sent"] != float64(1) {
		t.Fatal(sent)
	}
	s.deps.Benefits, _ = testbenefits.NewStore(db, "web_accounts")
	if replay := post("mail/preview", mail, "single-mail-key-01", 200); replay["id"] != single["id"] || replay["sent"] != float64(1) {
		t.Fatal("restart lost task", replay)
	}
	run(single, false, 409)
	m, _, _ = g.Mailbox.Load(ctx, "0010")
	if len(m.Mails) != 2 {
		t.Fatal("duplicate single mail")
	}
	// Recipient deletion after preview is rejected at delivery; an explicit retry
	// after restoring the same incarnation commits exactly one mail.
	retryJob := post("mail/preview", mail, "single-retry-key-01", 200)
	role, _, err := g.Character.Load(ctx, "0010")
	if err != nil {
		t.Fatal(err)
	}
	if role.Stats == nil {
		role.Stats = map[string]int64{}
	}
	role.Stats["delete_flag"] = 1
	if err = g.Character.Save(ctx, role); err != nil {
		t.Fatal(err)
	}
	failed := run(retryJob, false, 200)
	if failed["failed"] != float64(1) || failed["sent"] != float64(0) {
		t.Fatal("deleted recipient delivered", failed)
	}
	failedRows := get("tasks/" + retryJob["id"].(string) + "/recipients")["data"].([]any)
	if failedRows[0].(map[string]any)["failure"].(map[string]any)["code"] != "recipient_changed" {
		t.Fatal("missing actionable failure", failedRows)
	}
	role.Stats["delete_flag"] = 0
	if err = g.Character.Save(ctx, role); err != nil {
		t.Fatal(err)
	}
	completed := run(failed, true, 200)
	if completed["sent"] != float64(1) || completed["failed"] != float64(0) {
		t.Fatal("retry did not recover", completed)
	}
	run(failed, true, 409)
	m, _, _ = g.Mailbox.Load(ctx, "0010")
	if len(m.Mails) != 3 {
		t.Fatal("retry duplicated assets")
	}
	var database string
	if err = db.QueryRow("SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	process := exec.Command(os.Args[0], "-test.run=^TestV1OperationRestartReader$", "-test.v")
	process.Env = append(os.Environ(), "DNF90GM_V1_RESTART_DATABASE="+database, "DNF90GM_V1_RESTART_CAMPAIGN="+id)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("independent process persistence: %v\n%s", err, output)
	}
	// Page summaries and individual receipts; page size/resource bind the cursor.
	seen := map[string]bool{}
	path := "tasks?limit=1"
	for {
		out := get(path)
		for _, raw := range out["data"].([]any) {
			row := raw.(map[string]any)
			key := row["id"].(string)
			if seen[key] || row["recipients"] != nil || row["requestFingerprint"] != nil {
				t.Fatal("summary leaked/duplicated", row)
			}
			seen[key] = true
		}
		cursor, _ := out["page"].(map[string]any)["nextCursor"].(string)
		if cursor == "" {
			break
		}
		path = "tasks?limit=1&cursor=" + url.QueryEscape(cursor)
	}
	if len(seen) != 5 {
		t.Fatal("lost task pages", seen)
	}
	out := get("tasks/" + jobID + "/recipients?limit=1")
	rec := out["data"].([]any)[0].(map[string]any)
	if rec["mailId"] == "" || rec["deliveredCharacterId"] == "" {
		t.Fatal("missing receipt", rec)
	}
	cursor := out["page"].(map[string]any)["nextCursor"].(string)
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/tasks/"+single["id"].(string)+"/recipients?limit=1&cursor="+url.QueryEscape(cursor)); status != 400 {
		t.Fatal("cross-task cursor accepted")
	}
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/tasks/"+jobID+"/recipients?limit=2&cursor="+url.QueryEscape(cursor)); status != 400 {
		t.Fatal("changed page size accepted")
	}
	// Deletion keeps the original identity, completed jobs and permanent receipts.
	post(base+"/delete", `{"revision":1}`, "", 422)
	post(base+"/delete", `{"revision":0,"confirm":true}`, "", 409)
	deleted := post(base+"/delete", `{"revision":1,"confirm":true}`, "", 200)
	if deleted["deleted"] != true || deleted["enabled"] != false {
		t.Fatal("rule still active", deleted)
	}
	if again := post(base+"/delete", `{"revision":1,"confirm":true}`, "", 200); again["revision"] != deleted["revision"] {
		t.Fatal("delete retry changed revision", again)
	}
	post(base+"/state", `{"revision":2,"enabled":true,"confirm":true}`, "", 409)
	post(base+"/preview", `{}`, "deleted-preview-key", 409)
	if replay := post("benefits", create, "concurrent-rule-key", 200); replay["id"] != id || replay["deleted"] != true {
		t.Fatal("old create resurrected deleted rule", replay)
	}
	for _, raw := range get("benefits?limit=1")["data"].([]any) {
		if raw.(map[string]any)["id"] == id {
			t.Fatal("deleted rule remained in first page")
		}
	}
	rows, err := store.Campaigns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			t.Fatal("deleted rule remained in scanner")
		}
	}
	if saved := get("tasks/" + jobID)["data"].(map[string]any); saved["sent"] != float64(28) {
		t.Fatal("delete erased job progress", saved)
	}
	if saved := get("tasks/" + jobID + "/recipients?limit=1")["data"].([]any)[0].(map[string]any); saved["mailId"] != rec["mailId"] {
		t.Fatal("delete erased receipt", saved)
	}
}

func TestV1OperationRestartReader(t *testing.T) {
	database := os.Getenv("DNF90GM_V1_RESTART_DATABASE")
	if database == "" {
		t.Skip("child process of disposable fixture only")
	}
	if !strings.HasPrefix(database, "gm_v1_test_") {
		t.Fatal("invalid disposable target")
	}
	cfg, err := driver.ParseDSN(os.Getenv("DNFBRIDGE_PLAYER_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = database
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := testbenefits.NewStore(db, "web_accounts")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := os.Getenv("DNF90GM_V1_RESTART_CAMPAIGN")
	saved, err := store.Campaign(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateCampaignOnce(ctx, "concurrent-rule-key", saved.Reward, saved.ItemName, saved.AutoNewCharacters)
	if err != nil || replayed.ID != id {
		t.Fatal("restart created another campaign", err)
	}
	job, err := store.PreviewMailOnce(ctx, repository.Group{}, "single-mail-key-01", "dnf:1", "0010", testbenefits.Job{Title: "V1 single mail", Gold: 17})
	if err != nil || testbenefits.Summarize(job).Sent != 1 {
		t.Fatal("restart lost completed preview", err)
	}
}
