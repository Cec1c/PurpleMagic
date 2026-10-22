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
	"testing"
	"time"

	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	"dnf90.local/gateway/internal/servicehttp"
)

func TestV1BenefitReconciliationPVFMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		for _, scope := range []testbenefits.GrantScope{testbenefits.ScopeCharacter, testbenefits.ScopeAccount} {
			t.Run(fmt.Sprintf("%d/%s", shards, scope), func(t *testing.T) {
				s, _, open := currencyFixture(t, shards)
				ctx := context.Background()
				g := open()
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
				setPVF := func(hash string, durability int) {
					source := newItemCacheTestSource(readonlyTestSource{"equipment/weapon/test_sword.equ": fmt.Sprintf("[durability]\n%d\n", durability)})
					source.checksum = strings.Repeat(hash, 64)
					s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
					s.pvf = newPVFCache()
					s.items = newItemIndex("", s, func(string, ...any) {})
					s.items.setEntriesLocked(itemcfgTestEntries())
				}
				setPVF("a", 30)
				create := func(account, id string, slot int) {
					check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: account, CharacterID: id, Name: id, Slot: slot, CreatedAt: time.Now().UTC()}))
				}
				create("wallet:1", "10", 0)
				c, e := store.CreateCampaignDraftOnce(ctx, "auto-pvf-campaign-create", scope, true, testbenefits.MailDraft{Title: "福利", ItemID: "7001", Count: 1, PVFChecksum: strings.Repeat("a", 64)}, s.v1ProjectCampaignPlan)
				check(e)
				c, e = store.EnableAutomaticCampaign(ctx, c.ID, c.Revision, true)
				check(e)
				ch, _, e := g.Character.Load(ctx, "10")
				check(e)
				check(store.DeliverCampaignToNewCharacter(ctx, g, c.ID, ch, s.v1ProjectCampaignDelivery))
				before, _, e := g.Account.Load(ctx, "wallet:1")
				check(e)
				oldMail, _, e := g.Mailbox.Load(ctx, "10")
				check(e)
				setPVF("b", 30)
				// Same script, same recipient intent: rebind the archive hash while
				// keeping the permanent receipt key and fingerprint unchanged.
				create("wallet:1", "11", 1)
				create("wallet:2", "20", 0)
				hash, token := s.v1PVFChecksum(), strings.Repeat("t", 48)
				handler := s.GatewayHandler(token, hash)
				lost := true
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if lost {
						lost = false
						rec := httptest.NewRecorder()
						handler.ServeHTTP(rec, r)
						if rec.Code != 200 {
							t.Error(rec.Code, rec.Body.String())
						}
						http.Error(w, "committed response lost", 503)
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
				q.ConfigureBenefitReconciliation(true)
				now := time.Now().UTC()
				if _, e = q.stepBenefitReconciliation(ctx, now, nil); e == nil {
					t.Fatal("loss not exercised")
				}
				q, e = OpenExternalQueue(path, peer)
				check(e)
				q.ConfigureBenefitReconciliation(true)
				finish := func(at time.Time) {
					t.Helper()
					for i := 0; i < 100; i++ {
						_, e = q.stepBenefitReconciliation(ctx, at, nil)
						check(e)
						if q.state.Reconciliation.Status == "waiting" {
							return
						}
					}
					t.Fatal("cycle did not finish")
				}
				finish(now.Add(time.Minute))
				updated, e := store.Campaign(ctx, c.ID)
				check(e)
				if updated.ID != c.ID || updated.Revision != c.Revision+1 || updated.Reward.PVFChecksum != hash || updated.Reward.ReceiptBasis == nil {
					t.Fatal(updated)
				}
				history, e := store.CampaignPVFHistory(ctx, c.ID, 0, 10)
				check(e)
				if len(history) != 1 {
					t.Fatal("auto review audit missing", history)
				}
				after, _, e := g.Account.Load(ctx, "wallet:1")
				check(e)
				for k, v := range before.Metadata {
					if after.Metadata[k] != v {
						t.Fatal("old metadata changed", k)
					}
				}
				box, _, e := g.Mailbox.Load(ctx, "10")
				check(e)
				if !reflect.DeepEqual(oldMail, box) {
					t.Fatal("old mail changed")
				}
				want := 1
				if scope == testbenefits.ScopeAccount {
					want = 0
				}
				box, _, e = g.Mailbox.Load(ctx, "11")
				check(e)
				if len(box.Mails) != want {
					t.Fatal("sibling scope", len(box.Mails), want)
				}
				// A new account with no character joins a later cycle. An old account's
				// second character also joins, using exactly the same scheduler path.
				_, e = s.db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES(3,'wallet:3','newaccount')")
				check(e)
				check(g.Account.Save(ctx, repository.AccountRecord{AccountID: "wallet:3"}))
				finish(q.state.Reconciliation.NextAt)
				create("wallet:3", "30", 0)
				create("wallet:2", "21", 1)
				finish(q.state.Reconciliation.NextAt)
				box, _, e = g.Mailbox.Load(ctx, "30")
				check(e)
				if len(box.Mails) != 1 {
					t.Fatal("new account first role missed")
				}
				box, _, e = g.Mailbox.Load(ctx, "21")
				check(e)
				if len(box.Mails) != want {
					t.Fatal("old account new role scope")
				}
				finish(q.state.Reconciliation.NextAt)
				box, _, e = g.Mailbox.Load(ctx, "30")
				check(e)
				if len(box.Mails) != 1 {
					t.Fatal("periodic duplicate")
				}
				setPVF("c", 45)
				create("wallet:3", "31", 1)
				out, e := s.scanBenefitPage(ctx, benefitScanPosition{})
				check(e)
				if len(out.Blocked) != 1 || out.Blocked[0].CampaignID != c.ID {
					t.Fatal("changed definition not blocked", out)
				}
				unchanged, e := store.Campaign(ctx, c.ID)
				check(e)
				if unchanged.Revision != updated.Revision {
					t.Fatal("changed reward automatically adopted")
				}
				box, _, e = g.Mailbox.Load(ctx, "31")
				check(e)
				if len(box.Mails) != 0 {
					t.Fatal("changed item delivered")
				}
				// Missing historical item evidence also cannot silently adopt a new PVF.
				unchanged.Reward.Definition = nil
				raw, e := json.Marshal(unchanged)
				check(e)
				_, e = s.db.Exec("UPDATE web_accounts_benefit_v2_campaigns SET payload=? WHERE id=?", raw, c.ID)
				check(e)
				setPVF("d", 30)
				out, e = s.scanBenefitPage(ctx, benefitScanPosition{})
				check(e)
				if len(out.Blocked) != 1 {
					t.Fatal("missing baseline auto accepted")
				}
			})
		}
	}
}
