package gmtool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

func TestV1CeraBenefitMySQL(t *testing.T) {
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
			store, err := testbenefits.NewStore(s.db, "web_accounts")
			check(err)
			check(store.EnsureSchema(ctx))
			s.deps.Benefits = store
			for i, id := range []string{"10", "11", "20"} {
				account := "wallet:1"
				if id == "20" {
					account = "wallet:2"
				}
				check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: account, CharacterID: id, Name: id, Slot: i, CreatedAt: time.Now().UTC().Add(-time.Hour)}))
			}
			token, err := s.startSession()
			check(err)
			post := func(path string, body any, key string, want int) map[string]any {
				t.Helper()
				raw, e := json.Marshal(body)
				check(e)
				status, out := operationCall(s, token, path, string(raw), key, nil)
				if status != want {
					t.Fatal(path, status, out)
				}
				data, _ := out["data"].(map[string]any)
				return data
			}
			payload := map[string]any{"scope": "account", "title": "点券福利", "cera": 250, "autoNewCharacters": true}
			created := post("benefits", payload, "cera-benefit-create", 200)
			id := created["id"].(string)
			if created["reward"].(map[string]any)["cera"] != float64(250) || post("benefits", payload, "cera-benefit-create", 200)["id"] != id {
				t.Fatal(created)
			}
			for field, value := range map[string]any{"scope": "character", "cera": -1, "gold": 1, "itemId": "1", "count": 1, "options": map[string]any{}, "pvfChecksum": "bad"} {
				bad := make(map[string]any)
				for k, v := range payload {
					bad[k] = v
				}
				bad[field] = value
				post("benefits", bad, "invalid-cera-"+field, 422)
			}
			payload["cera"] = 251
			post("benefits", payload, "cera-benefit-create", 409)
			payload["cera"] = 250
			for _, table := range []string{"web_accounts_benefit_campaigns", "web_accounts_benefit_acct_campaigns", "web_accounts_benefit_v2_campaigns", "web_accounts_benefit_v3_campaigns"} {
				var n int
				check(s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&n))
				want := 0
				if table == "web_accounts_benefit_v3_campaigns" {
					want = 1
				}
				if n != want {
					t.Fatal("currency table isolation", table, n)
				}
			}
			post("benefits/"+id+"/state", map[string]any{"revision": 0, "enabled": true, "confirm": true}, "", 200)
			job, err := store.PreviewCampaignOnce(ctx, g, id, "cera-preview-first")
			check(err)
			if len(job.Recipients) != 2 || job.Cera != 250 {
				t.Fatal(job)
			}
			result := post("tasks/"+job.ID+"/run", map[string]any{"revision": job.Revision, "confirm": true}, "", 200)
			if result["sent"] != float64(2) || result["reward"].(map[string]any)["cera"] != float64(250) {
				t.Fatal(result)
			}
			for range 2 {
				_, err = s.ScanNewCharacters(ctx, 0, 100)
				check(err)
			}
			for _, account := range []string{"wallet:1", "wallet:2"} {
				a, _, e := g.Account.Load(ctx, account)
				check(e)
				if a.Metadata["account_cera"] != "1250" || a.Metadata["gm_currency_sequence_v1"] != "1" || a.Metadata["keep"] != "preserved" {
					t.Fatal(a)
				}
			}
			job, err = store.Job(ctx, job.ID)
			check(err)
			for _, r := range job.Recipients {
				if r.Delivery == nil || r.Delivery.Before != "1000" || r.Delivery.After != "1250" {
					t.Fatal(r)
				}
				box, _, e := g.Mailbox.Load(ctx, r.CharacterID)
				check(e)
				mail := box.Mails[r.MailID]
				if len(box.Mails) != 1 || mail.Gold != 0 || len(mail.Attachments) != 0 {
					t.Fatal(box)
				}
				check(g.Mailbox.Save(ctx, repository.MailboxRecord{CharacterID: r.CharacterID}))
			}
			store, err = testbenefits.NewStore(s.db, "web_accounts")
			check(err)
			s.deps.Benefits = store
			again, err := store.PreviewCampaignOnce(ctx, open(), id, "cera-preview-restarted")
			check(err)
			if testbenefits.Summarize(again).Sent != 2 || again.Recipients[0].Delivery.Cera != 250 {
				t.Fatal(again)
			}
			post("benefits/"+id+"/state", map[string]any{"revision": 1, "enabled": false, "confirm": true}, "", 200)
			check(g.Account.Save(ctx, repository.AccountRecord{AccountID: "wallet:3"}))
			_, err = s.db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES(3,'wallet:3','new')")
			check(err)
			check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: "wallet:3", CharacterID: "30", Name: "future", CreatedAt: time.Now().UTC().Add(time.Second)}))
			_, err = s.ScanNewCharacters(ctx, 0, 100)
			check(err)
			a, _, err := g.Account.Load(ctx, "wallet:3")
			check(err)
			if a.Metadata["account_cera"] != "" {
				t.Fatal("paused rule delivered")
			}
			post("benefits/"+id+"/state", map[string]any{"revision": 2, "enabled": true, "confirm": true}, "", 200)
			for range 2 {
				_, err = s.ScanNewCharacters(ctx, 0, 100)
				check(err)
			}
			a, _, err = g.Account.Load(ctx, "wallet:3")
			check(err)
			if a.Metadata["account_cera"] != "250" {
				t.Fatal(a)
			}
			// One overflowing wallet must not commit a notice or receipt; retry
			// credits just that account, leaving already successful recipients alone.
			other := post("benefits", payload, "cera-independent-rule", 200)["id"].(string)
			_, err = store.EnableCampaign(ctx, other, 0, true)
			check(err)
			a, _, err = g.Account.Load(ctx, "wallet:1")
			check(err)
			a.Metadata["account_cera"] = strconv.FormatInt(math.MaxInt64, 10)
			check(g.Account.Save(ctx, a))
			job, err = store.PreviewCampaignOnce(ctx, g, other, "cera-overflow-preview")
			check(err)
			job, err = store.RunVersion(ctx, g, job.ID, false, job.Revision)
			check(err)
			if sum := testbenefits.Summarize(job); sum.Failed != 1 || sum.Sent != 2 {
				t.Fatal(sum)
			}
			box, _, err := g.Mailbox.Load(ctx, "10")
			check(err)
			if len(box.Mails) != 0 {
				t.Fatal("overflow left notice")
			}
			a, _, err = g.Account.Load(ctx, "wallet:1")
			check(err)
			a.Metadata["account_cera"] = "100"
			check(g.Account.Save(ctx, a))
			job, err = store.RunVersion(ctx, g, job.ID, true, job.Revision)
			check(err)
			if sum := testbenefits.Summarize(job); sum.Failed != 0 || sum.Sent != 3 {
				t.Fatal(sum)
			}
			a, _, err = g.Account.Load(ctx, "wallet:1")
			check(err)
			if a.Metadata["account_cera"] != "350" {
				t.Fatal(a)
			}
			a, _, err = g.Account.Load(ctx, "wallet:2")
			check(err)
			if a.Metadata["account_cera"] != "1500" {
				t.Fatal("successful recipient credited twice", a)
			}
		})
	}
}
