package testbenefits_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/characterdata"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	repomysql "dnf90.local/gateway/internal/modules/dnf/repository/mysql"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	driver "github.com/go-sql-driver/mysql"
)

func TestBenefitsMySQL(t *testing.T) {
	dsn := os.Getenv("DNFBRIDGE_PLAYER_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable MySQL DSN not configured")
	}
	ctx := context.Background()
	admin, err := sql.Open("mysql", dsn)
	require(t, err)
	defer admin.Close()
	name := fmt.Sprintf("benefits_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4")
	require(t, err)
	defer admin.Exec("DROP DATABASE `" + name + "`")
	cfg, err := driver.ParseDSN(dsn)
	require(t, err)
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	require(t, err)
	defer db.Close()
	plan := repository.DatabasePlan{ReadDatabases: []string{name}, WriteDatabases: []string{name}}
	require(t, repomysql.EnsureMySQLSchema(ctx, db, repomysql.SchemaOptions{AutoCreate: true, DatabasePlan: plan}))
	g, err := repomysql.NewMySQLGroupFromDB(db, repomysql.MySQLGroupOptions{DatabasePlan: plan})
	require(t, err)
	_, err = db.Exec("CREATE TABLE web_accounts (id INT PRIMARY KEY, account_id VARCHAR(64) NOT NULL)")
	require(t, err)
	s, err := testbenefits.NewStore(db, "web_accounts")
	require(t, err)
	require(t, s.EnsureSchema(ctx))
	p, err := s.Policy(ctx)
	require(t, err)
	if p.AccountCera != 0 || p.CharacterGold != 0 {
		t.Fatal(p)
	}
	p.AccountCera = 200
	p.CharacterGold = 300
	p, err = s.SavePolicy(ctx, p)
	require(t, err)
	if _, err = s.SavePolicy(ctx, testbenefits.Policy{}); err == nil {
		t.Fatal("stale revision accepted")
	}
	// Entitlement and registration must disappear together on rollback.
	tx, err := db.BeginTx(ctx, nil)
	require(t, err)
	require(t, s.CaptureSignup(ctx, tx, "rolled-back"))
	require(t, tx.Rollback())
	amount, err := s.SignupCera(ctx, "rolled-back")
	require(t, err)
	if amount != 0 {
		t.Fatal(amount)
	}
	tx, err = db.BeginTx(ctx, nil)
	require(t, err)
	require(t, s.CaptureSignup(ctx, tx, "new"))
	require(t, tx.Commit())
	p.AccountCera = 900
	p, err = s.SavePolicy(ctx, p)
	require(t, err)
	amount, err = s.SignupCera(ctx, "new")
	require(t, err)
	if amount != 200 {
		t.Fatal("signup snapshot changed")
	}
	old, err := s.SignupCera(ctx, "old")
	require(t, err)
	if old != 0 {
		t.Fatal("old account granted")
	}
	_, err = db.Exec("INSERT INTO web_accounts VALUES (1,'new')")
	require(t, err)
	require(t, g.Account.Save(ctx, repository.AccountRecord{AccountID: "new"}))
	creator := characterdata.NewCreator(g)
	for i := 1; i <= 12; i++ {
		id := fmt.Sprint(i)
		require(t, creator.Create(ctx, characterdata.Creation{
			Account: &repository.AccountRecord{AccountID: "new"}, WelcomeCera: amount,
			Character: repository.CharacterRecord{CharacterID: id, AccountID: "new", Slot: i - 1, Name: "fixture" + id, CreatedAt: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC), Stats: map[string]int64{"gold": p.CharacterGold}},
		}))
	}
	a, _, err := g.Account.Load(ctx, "new")
	require(t, err)
	if a.Metadata["account_cera"] != "200" {
		t.Fatal("repeated welcome grant")
	}
	j, err := s.Preview(ctx, g, testbenefits.Job{Gold: 123, Title: "SQL fixture", Body: "test"})
	require(t, err)
	if len(j.Recipients) != 12 {
		t.Fatal(len(j.Recipients))
	}
	m, _, err := g.Mailbox.Load(ctx, "1")
	require(t, err)
	if len(m.Mails) != 0 {
		t.Fatal("preview delivered mail")
	}
	// Simulate crash after recipient asset commit but before job progress commit.
	_, err = testbenefits.Deliver(ctx, g, j, j.Recipients[0])
	require(t, err)
	s, err = testbenefits.NewStore(db, "web_accounts")
	require(t, err)
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Run(ctx, g, j.ID, false)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	jobs, err := s.Jobs(ctx)
	require(t, err)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	for _, r := range jobs[0].Recipients {
		if r.MailID == "" || r.Error != "" {
			t.Fatal(r)
		}
		m, _, err = g.Mailbox.Load(ctx, r.CharacterID)
		require(t, err)
		if len(m.Mails) != 1 || m.Mails[r.MailID].Gold != 123 {
			t.Fatal("duplicate or missing mail")
		}
	}
	// A lost progress record followed by retry still cannot duplicate money.
	raw, err := json.Marshal(j)
	require(t, err)
	_, err = db.Exec("UPDATE web_accounts_benefit_jobs SET payload=? WHERE id=?", string(raw), j.ID)
	require(t, err)
	_, err = s.Run(ctx, g, j.ID, true)
	require(t, err)
	m, _, err = g.Mailbox.Load(ctx, j.Recipients[0].CharacterID)
	require(t, err)
	if len(m.Mails) != 1 {
		t.Fatal("retry duplicated mail")
	}
	// A deletion between preview and execute must fail just that recipient.
	j2, err := s.Preview(ctx, g, testbenefits.Job{Gold: 25, Title: "retry fixture"})
	require(t, err)
	deleted, _, err := g.Character.Load(ctx, "12")
	require(t, err)
	deleted.Stats["delete_flag"] = 1
	require(t, repository.SaveCharacterFields(ctx, g.Character, deleted, repository.CharacterFieldStats))
	for range 2 {
		_, err = s.Run(ctx, g, j2.ID, false)
		require(t, err)
	}
	jobs, err = s.Jobs(ctx)
	require(t, err)
	failed := 0
	for _, r := range jobs[0].Recipients {
		if r.Error != "" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed recipients=%d", failed)
	}
	deleted.Stats["delete_flag"] = 0
	require(t, repository.SaveCharacterFields(ctx, g.Character, deleted, repository.CharacterFieldStats))
	retried, err := s.Run(ctx, g, j2.ID, true)
	require(t, err)
	for _, r := range retried.Recipients {
		if r.MailID == "" || r.Error != "" {
			t.Fatal(r)
		}
	}

	// Reusable item rules: all old characters can be backfilled, but only
	// characters born after activation are automatically eligible.
	campaign, err := s.CreateCampaign(ctx, testbenefits.Job{Title: "item fixture", Attachments: []repository.MailAttachment{{ItemID: 490009735, Count: 1}}}, "fixture box", true)
	require(t, err)
	if campaign.Enabled || !campaign.FirstEnabledAt.IsZero() {
		t.Fatal("new rule was active")
	}
	preview, err := s.PreviewCampaign(ctx, g, campaign.ID)
	require(t, err)
	if _, err = s.Run(ctx, g, preview.ID, false); err == nil {
		t.Fatal("disabled rule sent mail")
	}
	campaign, err = s.EnableCampaign(ctx, campaign.ID, campaign.Revision, true)
	require(t, err)
	if _, err = s.EnableCampaign(ctx, campaign.ID, 0, false); err == nil {
		t.Fatal("stale revision accepted")
	}
	oldChar, _, err := g.Character.Load(ctx, "1")
	require(t, err)
	before, _, err := g.Mailbox.Load(ctx, "1")
	require(t, err)
	require(t, s.DeliverNewCharacter(ctx, g, oldChar))
	after, _, err := g.Mailbox.Load(ctx, "1")
	require(t, err)
	if len(before.Mails) != len(after.Mails) {
		t.Fatal("old role automatically granted")
	}
	newChar := repository.CharacterRecord{AccountID: "new", CharacterID: "13", Slot: 12, Name: "new fixture", CreatedAt: campaign.FirstEnabledAt.Add(time.Second)}
	require(t, g.Character.Save(ctx, newChar))
	newChar, _, err = g.Character.Load(ctx, newChar.CharacterID)
	require(t, err)
	preview, err = s.PreviewCampaign(ctx, g, campaign.ID)
	require(t, err)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.DeliverNewCharacter(ctx, g, newChar); e != nil {
				t.Error(e)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Run(ctx, g, preview.ID, false); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	after, _, err = g.Mailbox.Load(ctx, "13")
	require(t, err)
	if len(after.Mails) != 1 || after.Mails["1"].Attachments[0].ItemID != 490009735 {
		t.Fatal("manual/automatic delivery duplicated", after)
	}
	require(t, g.Mailbox.Save(ctx, repository.MailboxRecord{CharacterID: "13"}))
	s, err = testbenefits.NewStore(db, "web_accounts")
	require(t, err)
	require(t, s.DeliverNewCharacter(ctx, g, newChar))
	preview, err = s.PreviewCampaign(ctx, g, campaign.ID)
	require(t, err)
	for _, r := range preview.Recipients {
		if r.MailID == "" {
			t.Fatal("new preview lost durable receipt", r)
		}
	}
	campaign, err = s.EnableCampaign(ctx, campaign.ID, campaign.Revision, false)
	require(t, err)
	newChar.CharacterID = "14"
	newChar.Slot = 13
	require(t, g.Character.Save(ctx, newChar))
	require(t, s.DeliverNewCharacter(ctx, g, newChar))
	after, _, err = g.Mailbox.Load(ctx, "14")
	require(t, err)
	if len(after.Mails) != 0 {
		t.Fatal("disabled automatic rule sent mail")
	}
	first := campaign.FirstEnabledAt
	campaign, err = s.EnableCampaign(ctx, campaign.ID, campaign.Revision, true)
	require(t, err)
	if !campaign.FirstEnabledAt.Equal(first) {
		t.Fatal("resume reset cutoff")
	}
	require(t, s.DeliverNewCharacter(ctx, g, newChar))
	after, _, err = g.Mailbox.Load(ctx, "14")
	require(t, err)
	if len(after.Mails) != 1 {
		t.Fatal("resume failed")
	}
	testAccountScopeMySQL(t, ctx, db, g, s)
	testDeletedCampaignMySQL(t, ctx, g, s)
}

func testDeletedCampaignMySQL(t *testing.T, ctx context.Context, g repository.Group, s *testbenefits.Store) {
	t.Helper()
	c, err := s.CreateCampaign(ctx, testbenefits.Job{Gold: 15, Title: "delete fixture"}, "delete fixture", true)
	require(t, err)
	c, err = s.EnableAutomaticCampaign(ctx, c.ID, c.Revision, true)
	require(t, err)
	j, err := s.PreviewCampaign(ctx, g, c.ID)
	require(t, err)
	before, _, err := g.Mailbox.Load(ctx, "1")
	require(t, err)
	c, err = s.DeleteCampaign(ctx, c.ID, c.Revision)
	require(t, err)
	if _, err = s.Run(ctx, g, j.ID, false); err == nil {
		t.Fatal("queued task delivered deleted rule")
	}
	role, _, err := g.Character.Load(ctx, "1")
	require(t, err)
	if testbenefits.EligibleNewCharacter(c, role) {
		t.Fatal("deleted rule eligible")
	}
	if _, err = s.RedeliverCampaignRecipient(ctx, g, c.ID, c.Revision, j.Recipients[0], nil); err == nil {
		t.Fatal("redelivery accepted deleted rule")
	}
	after, _, err := g.Mailbox.Load(ctx, "1")
	require(t, err)
	if len(before.Mails) != len(after.Mails) {
		t.Fatal("deleted rule changed mailbox")
	}
}

func testAccountScopeMySQL(t *testing.T, ctx context.Context, db *sql.DB, g repository.Group, s *testbenefits.Store) {
	t.Helper()
	campaign, err := s.CreateCampaign(ctx, testbenefits.Job{Title: "account once", Scope: testbenefits.ScopeAccount, Attachments: []repository.MailAttachment{{ItemID: 490009735, Count: 1}}}, "scope fixture", true)
	require(t, err)
	campaign, err = s.EnableCampaign(ctx, campaign.ID, campaign.Revision, true)
	require(t, err)
	var roles []repository.CharacterRecord
	for i := 15; i <= 16; i++ {
		c := repository.CharacterRecord{AccountID: "new", CharacterID: fmt.Sprint(i), Slot: i - 1, Name: fmt.Sprintf("scope fixture%d", i), CreatedAt: campaign.FirstEnabledAt.Add(time.Second)}
		require(t, g.Character.Save(ctx, c))
		c, _, err = g.Character.Load(ctx, c.CharacterID)
		require(t, err)
		roles = append(roles, c)
	}
	preview, err := s.PreviewCampaign(ctx, g, campaign.ID)
	require(t, err)
	if preview.Scope != testbenefits.ScopeAccount || len(preview.Recipients) != 1 || preview.Recipients[0].CharacterID != "1" {
		t.Fatalf("account preview must freeze one target: %+v", preview.Recipients)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%3 == 0 {
				if _, err := s.Run(ctx, g, preview.ID, false); err != nil {
					t.Error(err)
				}
			} else if err := s.DeliverNewCharacter(ctx, g, roles[i%2]); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	count := 0
	var deliveredTo string
	for _, id := range []string{"1", "15", "16"} {
		box, _, err := g.Mailbox.Load(ctx, id)
		require(t, err)
		for _, mail := range box.Mails {
			if mail.Title == "account once" {
				count++
				deliveredTo = id
			}
		}
	}
	if count != 1 {
		t.Fatalf("account manual/automatic races sent %d mails", count)
	}
	// Simulate a lost progress commit, then restart the store and replay.
	preview.Started = false
	raw, err := json.Marshal(preview)
	require(t, err)
	_, err = db.ExecContext(ctx, "UPDATE web_accounts_benefit_acct_jobs SET payload=? WHERE id=?", raw, preview.ID)
	require(t, err)
	s, err = testbenefits.NewStore(db, "web_accounts")
	require(t, err)
	result, err := s.Run(ctx, g, preview.ID, false)
	require(t, err)
	if len(result.Recipients) != 1 || result.Recipients[0].DeliveredCharacterID != deliveredTo || result.Recipients[0].MailID == "" {
		t.Fatal("progress retry lost actual delivery target", result.Recipients)
	}
	// The receipt survives mailbox deletion and new previews.
	require(t, g.Mailbox.Save(ctx, repository.MailboxRecord{CharacterID: deliveredTo}))
	preview, err = s.PreviewCampaign(ctx, g, campaign.ID)
	require(t, err)
	if len(preview.Recipients) != 1 || preview.Recipients[0].DeliveredCharacterID != deliveredTo || preview.Recipients[0].MailID == "" {
		t.Fatal("new account preview lost permanent receipt", preview.Recipients)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM web_accounts_benefit_campaigns WHERE id='" + campaign.ID + "'",
		"SELECT COUNT(*) FROM web_accounts_benefit_jobs WHERE id='" + preview.ID + "'",
	} {
		var legacyVisible int
		require(t, db.QueryRowContext(ctx, query).Scan(&legacyVisible))
		if legacyVisible != 0 {
			t.Fatal("older workers can see account-scope records")
		}
	}
}
