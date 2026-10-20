package gmtool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	"dnf90.local/gateway/internal/servicehttp"
)

func TestV1BenefitRedeliveryMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			ctx, g := context.Background(), open()
			check := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			store, e := testbenefits.NewStore(s.db, "web_accounts")
			check(e)
			check(store.EnsureSchema(ctx))
			s.deps.Benefits = store
			character := func(account, id string, slot int, at time.Time) {
				check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: account, CharacterID: id, Name: "test-" + id, Slot: slot, CreatedAt: at}))
			}
			past := time.Now().UTC().Add(-time.Hour)
			character("wallet:1", "100", 0, past)
			character("wallet:1", "101", 1, past)
			character("wallet:2", "200", 0, past)
			old := repository.MailboxRecord{CharacterID: "100", Mails: map[string]repository.MailRecord{}}
			for i := 1; i <= 104; i++ {
				id := fmt.Sprint(i)
				old.Mails[id] = repository.MailRecord{MailID: id, RecipientCharacterID: "100", Title: "existing", Body: "preserve", CreatedAt: past, Attachments: []repository.MailAttachment{{ItemID: 42, Count: 3, Extra: map[string]string{"keep": "value"}}}, Metadata: map[string]string{"keep": "value"}}
			}
			check(g.Mailbox.Save(ctx, old))
			before, _, e := g.Mailbox.Load(ctx, "100")
			check(e)
			// This reproduces a large real mailbox while making any aggregate rewrite
			// fail immediately. Old mail, attachment and metadata rows must survive.
			var database string
			check(s.db.QueryRow("SELECT DATABASE()").Scan(&database))
			rows, e := s.db.Query("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE ?", strings.TrimSuffix(database, "_0")+"_%")
			check(e)
			var names []string
			for rows.Next() {
				var n string
				check(rows.Scan(&n))
				names = append(names, n)
			}
			check(rows.Close())
			for _, n := range names {
				for _, table := range []string{"dnf_mails", "dnf_mail_metadata", "dnf_mail_attachments", "dnf_mail_attachment_extra", "dnf_account_metadata"} {
					_, e = s.db.Exec("CREATE TRIGGER " + n + ".protect_" + table + " BEFORE DELETE ON " + n + "." + table + " FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='aggregate rewrite forbidden'")
					check(e)
				}
			}
			create := func(cera bool, future bool) testbenefits.Campaign {
				reward := testbenefits.Job{Title: "test", Gold: 7, Scope: testbenefits.ScopeCharacter}
				if cera {
					reward.Gold = 0
					reward.Cera = 25
					reward.Scope = testbenefits.ScopeAccount
				}
				c, e := store.CreateCampaign(ctx, reward, "", future)
				check(e)
				c, e = store.EnableAutomaticCampaign(ctx, c.ID, c.Revision, true)
				check(e)
				return c
			}
			gold, cera, existing := create(false, true), create(true, true), create(false, false)
			blocked, calls := true, 0
			guarded := g
			guarded.MailboxAssets = blockedBenefitDelivery{g.MailboxAssets, g.MailboxAssets.(repository.AccountMailboxCharacterUnitOfWork), &blocked, &calls}
			s.deps.Repos = func() (repository.Group, bool) { return guarded, true }
			hash, token := strings.Repeat("a", 64), strings.Repeat("t", 48)
			lost := true
			handler := s.GatewayHandler(token, hash)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if lost && strings.HasSuffix(r.URL.Path, "/redelivery") {
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, r)
					var v benefitRedeliveryResult
					json.Unmarshal(rec.Body.Bytes(), &v)
					if v.Received > 0 {
						lost = false
						http.Error(w, "lost batch response", 503)
						return
					}
					for k, values := range rec.Header() {
						w.Header()[k] = values
					}
					w.WriteHeader(rec.Code)
					w.Write(rec.Body.Bytes())
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			peer, e := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: hash})
			check(e)
			path := filepath.Join(t.TempDir(), "queue.json")
			q, e := OpenExternalQueue(path, peer)
			check(e)
			start := func(c testbenefits.Campaign, key string, want int) queuedBenefit {
				t.Helper()
				body := fmt.Sprintf(`{"revision":%d,"confirm":true}`, c.Revision)
				r := httptest.NewRequest("POST", "/api/v1/benefits/"+c.ID+"/redelivery", strings.NewReader(body))
				r.Header.Set("Idempotency-Key", key)
				w := httptest.NewRecorder()
				if !q.HandleBenefit(w, r) || w.Code != want {
					t.Fatal(w.Code, w.Body.String())
				}
				var v struct{ Data queuedBenefit }
				check(json.Unmarshal(w.Body.Bytes(), &v))
				return v.Data
			}
			first := start(gold, "redelivery-first-001", 200)
			start(gold, "redelivery-double-001", 409)
			start(cera, "redelivery-first-001", 409)
			if calls != 0 {
				t.Fatal("schedule delivered synchronously")
			}
			check(q.stepBenefits(ctx))
			if e = q.stepBenefits(ctx); e == nil {
				t.Fatal("response loss not tested")
			}
			q, e = OpenExternalQueue(path, peer)
			check(e)
			if start(gold, "redelivery-first-001", 200).ID != first.ID {
				t.Fatal("lost schedule response created job")
			}
			finish := func() {
				for i := 0; i < 30; i++ {
					check(q.stepBenefits(ctx))
					active := false
					for _, j := range q.state.Benefits {
						active = active || j.Status == "active"
					}
					if !active {
						return
					}
				}
				t.Fatal("worker did not finish")
			}
			finish()
			j := q.latestBenefit(gold.ID)
			if j.Status != "failed" || j.Failed != 1 || j.Received != 2 {
				t.Fatal(j)
			}
			blocked = false
			start(gold, "redelivery-retry-002", 200)
			finish()
			j = q.latestBenefit(gold.ID)
			if j.Status != "completed" || j.Received != 3 || j.Checked != 3 {
				t.Fatal(j)
			}
			after, _, e := g.Mailbox.Load(ctx, "100")
			check(e)
			if len(after.Mails) != 105 {
				t.Fatal("missing append", len(after.Mails))
			}
			for id, m := range before.Mails {
				if !reflect.DeepEqual(m, after.Mails[id]) {
					t.Fatal("old mail changed", id)
				}
			}
			// Replaying the first completed request must never start another scan.
			if start(gold, "redelivery-first-001", 200).ID != first.ID {
				t.Fatal("idempotency history lost")
			}
			start(cera, "redelivery-cera-003", 200)
			finish()
			start(cera, "redelivery-cera-004", 200)
			finish()
			for _, id := range []string{"wallet:1", "wallet:2"} {
				a, _, e := g.Account.Load(ctx, id)
				check(e)
				if a.Metadata["account_cera"] != "1025" || a.Metadata["gm_currency_sequence_v1"] != "1" || a.Metadata["keep"] != "preserved" {
					t.Fatal(a)
				}
			}
			// Pause/revision changes interrupt accepted work before a new delivery.
			start(gold, "redelivery-pause-005", 200)
			gold, e = store.EnableAutomaticCampaign(ctx, gold.ID, gold.Revision, false)
			check(e)
			finish()
			if q.latestBenefit(gold.ID).Status != "conflict" {
				t.Fatal("paused batch continued")
			}
			start(gold, "redelivery-paused-006", 409)
			gold, e = store.EnableAutomaticCampaign(ctx, gold.ID, gold.Revision, true)
			check(e)
			// Automatic scanning must keep enrolling future roles and new accounts.
			s.deps.Repos = func() (repository.Group, bool) { return g, true }
			scan := func() {
				p := benefitScanPosition{}
				for i := 0; i < 30; i++ {
					var e error
					p, e = s.scanBenefitDeliveries(ctx, p)
					check(e)
					if p.Account == 0 {
						return
					}
				}
				t.Fatal("scan did not finish")
			}
			scan()
			for i := 3; i <= 4; i++ {
				id := fmt.Sprintf("wallet:%d", i)
				_, e = s.db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES(?,?,?)", i, id, id)
				check(e)
				check(g.Account.Save(ctx, repository.AccountRecord{AccountID: id, Metadata: map[string]string{"account_cera": "1000"}}))
			}
			now := time.Now().UTC()
			character("wallet:1", "102", 2, now)
			character("wallet:3", "300", 0, now)
			character("wallet:3", "301", 1, now)
			// A concurrent manual scan and automatic cycle use the same receipts.
			start(cera, "redelivery-concurrent-007", 200)
			var wg sync.WaitGroup
			wg.Add(1)
			go func() { defer wg.Done(); scan() }()
			finish()
			wg.Wait()
			scan()
			for _, id := range []string{"102", "300", "301"} {
				box, _, e := g.Mailbox.Load(ctx, id)
				check(e)
				goldCount := 0
				for _, m := range box.Mails {
					if m.Gold == 7 {
						goldCount++
					}
				}
				if goldCount != 1 {
					t.Fatal("future role not delivered once", id, goldCount)
				}
			}
			for _, id := range []string{"wallet:1", "wallet:2", "wallet:3", "wallet:4"} {
				a, _, e := g.Account.Load(ctx, id)
				check(e)
				want := "1025"
				if id == "wallet:4" {
					want = "1000"
				}
				if a.Metadata["account_cera"] != want {
					t.Fatal("future account credit", id, a.Metadata["account_cera"])
				}
			}
			character("wallet:4", "400", 0, time.Now().UTC())
			scan()
			scan()
			a, _, e := g.Account.Load(ctx, "wallet:4")
			check(e)
			if a.Metadata["account_cera"] != "1025" {
				t.Fatal("new account first character missed")
			}
			page, _, e := store.CampaignRecipientPage(ctx, g, existing, "", 100)
			check(e)
			for _, r := range page {
				if r.CharacterID == "102" && (r.Eligible || r.MailID != "") {
					t.Fatal("existing-only scope expanded")
				}
			}
			// Hash changes retain audit records but stop accepted work on restart.
			start(gold, "redelivery-hash-008", 200)
			nextPeer, e := servicehttp.New(servicehttp.Config{URL: server.URL, Token: token, PVFSHA256: strings.Repeat("b", 64)})
			check(e)
			q, e = OpenExternalQueue(path, nextPeer)
			check(e)
			if q.latestBenefit(gold.ID).Status != "conflict" {
				t.Fatal("old PVF resumed")
			}
		})
	}
}
