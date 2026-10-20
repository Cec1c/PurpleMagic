package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

type benefitSummaryOnly struct {
	repository.CharacterRepository
	repository.CharacterSummaryReader
}

func (benefitSummaryOnly) Load(context.Context, string) (repository.CharacterRecord, bool, error) {
	return repository.CharacterRecord{}, false, errors.New("unexpected full character load")
}
func (benefitSummaryOnly) ListByAccount(context.Context, string, int) ([]repository.CharacterRecord, error) {
	return nil, errors.New("unexpected full character list")
}

type blockedBenefitDelivery struct {
	repository.MailboxAssetUnitOfWork
	base    repository.AccountMailboxCharacterUnitOfWork
	blocked *bool
	calls   *int
}

func (b blockedBenefitDelivery) WithinAccountMailboxCharacter(ctx context.Context, a, c string, fn func(repository.AccountRepository, repository.CharacterRecord, repository.MailboxRepository) error) error {
	*b.calls++
	if *b.blocked && c == "100" {
		return context.DeadlineExceeded
	}
	return b.base.WithinAccountMailboxCharacter(ctx, a, c, fn)
}

func TestV1BenefitProgressMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
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
			// More than one account page and more than one scan batch inside one account.
			for i := 1; i <= 8; i++ {
				account := fmt.Sprintf("wallet:%d", i)
				if i > 2 {
					_, e = s.db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES(?,?,?)", i, account, account)
					check(e)
					check(g.Account.Save(ctx, repository.AccountRecord{AccountID: account, Metadata: map[string]string{"account_cera": "1000"}}))
				}
				count := 1
				if i == 1 {
					count = 16
				}
				for j := 0; j < count; j++ {
					check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: account, CharacterID: fmt.Sprint(i*100 + j), Name: fmt.Sprintf("recipient-%d-%d", i, j), Slot: j, CreatedAt: time.Now().UTC().Add(-time.Hour)}))
				}
			}
			var rules []testbenefits.Campaign
			for i := 0; i < 3; i++ {
				c, e := store.CreateCampaign(ctx, testbenefits.Job{Gold: 7, Title: fmt.Sprint("rule", i)}, "", true)
				check(e)
				c, e = store.EnableAutomaticCampaign(ctx, c.ID, c.Revision, true)
				check(e)
				rules = append(rules, c)
			}
			c, e := store.CreateCampaign(ctx, testbenefits.Job{Scope: testbenefits.ScopeAccount, Cera: 25, Title: "account-cera"}, "", true)
			check(e)
			c, e = store.EnableAutomaticCampaign(ctx, c.ID, c.Revision, true)
			check(e)
			rules = append(rules, c)
			guarded := g
			guarded.Character = benefitSummaryOnly{g.Character, g.Character.(repository.CharacterSummaryReader)}
			blocked, calls := true, 0
			guarded.MailboxAssets = blockedBenefitDelivery{g.MailboxAssets, g.MailboxAssets.(repository.AccountMailboxCharacterUnitOfWork), &blocked, &calls}
			s.deps.Repos = func() (repository.Group, bool) { return guarded, true }
			scan := func() int {
				position := benefitScanPosition{}
				requests := 0
				for {
					requests++
					if requests > 100 {
						t.Fatal("scan did not finish", position)
					}
					position, _ = s.scanBenefitDeliveries(ctx, position)
					if position.Account == 0 {
						return requests
					}
				}
			}
			if scan() < 3 {
				t.Fatal("did not exercise partial-account cursor")
			}
			// One failing character must not starve later characters or accounts.
			box, _, e := g.Mailbox.Load(ctx, "800")
			check(e)
			if len(box.Mails) != 4 {
				t.Fatal("later account starved", len(box.Mails))
			}
			box, _, e = g.Mailbox.Load(ctx, "115")
			check(e)
			if len(box.Mails) != 3 {
				t.Fatal("later character starved", len(box.Mails))
			}
			blocked = false
			scan()
			beforeCalls := calls
			scan()
			if calls != beforeCalls {
				t.Fatal("delivered recipients entered write transactions again", calls, beforeCalls)
			}
			for i := 1; i <= 8; i++ {
				a, _, e := g.Account.Load(ctx, fmt.Sprintf("wallet:%d", i))
				check(e)
				if a.Metadata["account_cera"] != "1025" {
					t.Fatal("account reward duplicated", i, a.Metadata["account_cera"])
				}
			}
			// Mail deletion never invalidates a permanent delivery receipt.
			check(g.Mailbox.Save(ctx, repository.MailboxRecord{CharacterID: "100"}))
			token, e := s.startSession()
			check(e)
			get := func(path string, want int) map[string]any {
				t.Helper()
				r := httptest.NewRequest("GET", path, nil)
				r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
				w := httptest.NewRecorder()
				s.ManagementHandler("").ServeHTTP(w, r)
				if w.Code != want {
					t.Fatal(path, w.Code, w.Body.String())
				}
				var out map[string]any
				check(json.Unmarshal(w.Body.Bytes(), &out))
				return out
			}
			for _, rule := range rules {
				path := "/api/v1/benefits/" + rule.ID + "/recipients?limit=3"
				cursor := ""
				seen := map[string]bool{}
				pages := 0
				for {
					pages++
					if pages > 30 {
						t.Fatal("recipient page did not finish")
					}
					out := get(path+"&cursor="+url.QueryEscape(cursor), 200)
					for _, raw := range out["data"].([]any) {
						r := raw.(map[string]any)
						key := r["accountId"].(string)
						if rule.Reward.Scope.Effective() != testbenefits.ScopeAccount {
							key += "/" + r["characterId"].(string)
						}
						if seen[key] || r["state"] != "sent" {
							t.Fatal("incorrect receipt page", r)
						}
						seen[key] = true
					}
					cursor, _ = out["page"].(map[string]any)["nextCursor"].(string)
					if cursor == "" {
						break
					}
				}
				want := 23
				if rule.Reward.Scope.Effective() == testbenefits.ScopeAccount {
					want = 8
				}
				if len(seen) != want {
					t.Fatal("missing recipients", len(seen), want)
				}
			}
			jobs, e := store.Jobs(ctx)
			check(e)
			if len(jobs) != 0 {
				t.Fatal("read status created jobs")
			}
			// Pausing keeps the audience and receipt visibility; adding a future role is pending.
			check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: "wallet:2", CharacterID: "201", Name: "future", Slot: 2, CreatedAt: time.Now().UTC()}))
			paused, e := store.EnableAutomaticCampaign(ctx, rules[0].ID, rules[0].Revision, false)
			check(e)
			rows, _, e := store.CampaignRecipientPage(ctx, guarded, paused, "2:", 100)
			check(e)
			pending := false
			for _, r := range rows {
				if r.CharacterID == "201" {
					pending = r.Eligible && r.MailID == ""
				}
			}
			if !pending {
				t.Fatal("paused campaign lost pending audience")
			}
		})
	}
}
