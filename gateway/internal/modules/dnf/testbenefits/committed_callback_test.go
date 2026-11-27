package testbenefits

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/repository/memory"
)

// commitBoundaryDB is a tiny database/sql driver used to verify sequencing.
// The benefit store only needs one payload SELECT and an optional UPDATE; the
// mail itself is committed by the independently implemented repository UOW.
type commitBoundaryDB struct {
	mu        sync.Mutex
	payload   []byte
	wrote     bool
	committed bool
	commitErr error
}

func (d *commitBoundaryDB) executed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.wrote
}

func (d *commitBoundaryDB) isCommitted() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.committed
}

type commitBoundaryConnector struct{ db *commitBoundaryDB }

func (c commitBoundaryConnector) Connect(context.Context) (driver.Conn, error) {
	return &commitBoundaryConn{db: c.db}, nil
}

func (commitBoundaryConnector) Driver() driver.Driver { return commitBoundaryDriver{} }

type commitBoundaryDriver struct{}

func (commitBoundaryDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("commit-boundary test driver requires a connector")
}

type commitBoundaryConn struct{ db *commitBoundaryDB }

func (*commitBoundaryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("commit-boundary test driver does not prepare statements")
}
func (*commitBoundaryConn) Close() error { return nil }
func (c *commitBoundaryConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *commitBoundaryConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &commitBoundaryTx{db: c.db}, nil
}
func (c *commitBoundaryConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &commitBoundaryRows{raw: append([]byte(nil), c.db.payload...)}, nil
}
func (c *commitBoundaryConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if !strings.HasPrefix(strings.TrimSpace(query), "UPDATE ") {
		return nil, errors.New("unexpected commit-boundary SQL")
	}
	c.db.mu.Lock()
	c.db.wrote = true
	c.db.mu.Unlock()
	return driver.RowsAffected(1), nil
}

type commitBoundaryRows struct {
	raw  []byte
	done bool
}

func (*commitBoundaryRows) Columns() []string { return []string{"payload"} }
func (*commitBoundaryRows) Close() error      { return nil }
func (r *commitBoundaryRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.raw
	return nil
}

type commitBoundaryTx struct{ db *commitBoundaryDB }

func (*commitBoundaryTx) Rollback() error { return nil }
func (t *commitBoundaryTx) Commit() error {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if t.db.commitErr != nil {
		return t.db.commitErr
	}
	t.db.committed = true
	return nil
}

func openCommitBoundaryStore(t *testing.T, payload []byte) (*Store, *commitBoundaryDB) {
	t.Helper()
	state := &commitBoundaryDB{payload: payload}
	db := sql.OpenDB(commitBoundaryConnector{db: state})
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db, "web_accounts")
	if err != nil {
		t.Fatal(err)
	}
	return store, state
}

func campaignCommitFixture(t *testing.T) (repository.Group, Campaign, repository.CharacterRecord, time.Time) {
	t.Helper()
	ctx := context.Background()
	group := memory.NewMemoryGroup()
	created := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	character := repository.CharacterRecord{AccountID: "acct-1", CharacterID: "19", Name: "recipient", Slot: 0, CreatedAt: created}
	if err := group.Account.Save(ctx, repository.AccountRecord{AccountID: character.AccountID, Metadata: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if err := group.Character.Save(ctx, character); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 32)
	reward := Job{ID: id, CampaignID: id, Scope: ScopeCharacter, Title: "notification fixture", Gold: 1, CreatedAt: created}
	campaign := Campaign{ID: id, Revision: 3, CreatedAt: created, FirstEnabledAt: created.Add(-time.Hour), Enabled: true, AutoNewCharacters: true, Reward: reward}
	return group, campaign, character, created
}

func TestRunVersionProjectedCommittedCallsAfterOuterCommitWithReceiptCharacter(t *testing.T) {
	ctx := context.Background()
	group := memory.NewMemoryGroup()
	created := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	actualCreated := created.Add(time.Hour)
	account := repository.AccountRecord{AccountID: "acct-1", Metadata: map[string]string{}}
	if err := group.Account.Save(ctx, account); err != nil {
		t.Fatal(err)
	}
	for _, character := range []repository.CharacterRecord{
		{AccountID: account.AccountID, CharacterID: "8", CreatedAt: created},
		{AccountID: account.AccountID, CharacterID: "9", CreatedAt: actualCreated},
	} {
		if err := group.Character.Save(ctx, character); err != nil {
			t.Fatal(err)
		}
	}
	job := Job{ID: strings.Repeat("a", 32), Scope: ScopeAccount, Title: "receipt replay", Gold: 1, CreatedAt: created,
		Recipients: []Recipient{{AccountID: account.AccountID, CharacterID: "8", CreatedAt: created}}}
	receipt := DeliveryReceipt{MailID: "existing-mail", CharacterID: "9", CreatedAt: actualCreated}
	key, prefix := receiptIdentity(job, job.Recipients[0])
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	account.Metadata[key] = prefix + string(rawReceipt)
	if err = group.Account.Save(ctx, account); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	store, state := openCommitBoundaryStore(t, payload)
	called := false
	got, err := store.RunVersionProjectedCommitted(ctx, group, job.ID, false, 0, nil, func(_ context.Context, receipts []DeliveryReceipt) {
		called = true
		if !state.isCommitted() {
			t.Error("notification callback ran before outer job commit")
		}
		if len(receipts) != 1 || receipts[0].CharacterID != "9" || receipts[0].MailID != "existing-mail" {
			t.Errorf("callback did not receive authoritative receipt: %+v", receipts)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || !state.executed() || got.Recipients[0].DeliveredCharacterID != "9" {
		t.Fatalf("callback=%v update=%v job=%+v", called, state.executed(), got)
	}
}

func TestRunVersionProjectedCommittedSkipsCallbackWhenOuterCommitFails(t *testing.T) {
	ctx := context.Background()
	group, campaign, character, _ := campaignCommitFixture(t)
	job := campaign.Reward
	job.Recipients = []Recipient{{AccountID: character.AccountID, CharacterID: character.CharacterID, Name: character.Name, CreatedAt: character.CreatedAt}}
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	store, state := openCommitBoundaryStore(t, payload)
	state.commitErr = errors.New("commit failed")
	called := false
	if _, err = store.RunVersionProjectedCommitted(ctx, group, job.ID, false, 0, nil, func(context.Context, []DeliveryReceipt) {
		called = true
	}); err == nil {
		t.Fatal("outer commit failure was ignored")
	}
	if called || state.isCommitted() {
		t.Fatal("notification callback ran after a failed outer commit")
	}
}

func TestDeliverCampaignToNewCharacterCommittedCallsAfterCommit(t *testing.T) {
	ctx := context.Background()
	group, campaign, character, _ := campaignCommitFixture(t)
	payload, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	store, state := openCommitBoundaryStore(t, payload)
	called := false
	err = store.DeliverCampaignToNewCharacterCommitted(ctx, group, campaign.ID, character, nil, func(_ context.Context, receipt DeliveryReceipt) {
		called = true
		if !state.isCommitted() || receipt.CharacterID != character.CharacterID || receipt.MailID == "" {
			t.Errorf("callback boundary/receipt = committed:%v receipt:%+v", state.isCommitted(), receipt)
		}
	})
	if err != nil || !called {
		t.Fatalf("automatic delivery callback err=%v called=%v", err, called)
	}
}

func TestDeliverCampaignToNewCharacterCommittedSkipsCallbackWhenCommitFails(t *testing.T) {
	ctx := context.Background()
	group, campaign, character, _ := campaignCommitFixture(t)
	payload, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	store, state := openCommitBoundaryStore(t, payload)
	state.commitErr = errors.New("campaign commit failed")
	called := false
	err = store.DeliverCampaignToNewCharacterCommitted(ctx, group, campaign.ID, character, nil, func(context.Context, DeliveryReceipt) {
		called = true
	})
	if err == nil || called || state.isCommitted() {
		t.Fatalf("automatic callback ran after failed commit: err=%v called=%v committed=%v", err, called, state.isCommitted())
	}
}

func TestRedeliverCampaignRecipientCommittedCallsAfterCommit(t *testing.T) {
	ctx := context.Background()
	group, campaign, character, _ := campaignCommitFixture(t)
	payload, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	store, state := openCommitBoundaryStore(t, payload)
	recipient := Recipient{AccountID: character.AccountID, CharacterID: character.CharacterID, Name: character.Name, CreatedAt: character.CreatedAt}
	called := false
	_, err = store.RedeliverCampaignRecipientCommitted(ctx, group, campaign.ID, campaign.Revision, recipient, nil, func(_ context.Context, receipt DeliveryReceipt) {
		called = true
		if !state.isCommitted() || receipt.CharacterID != character.CharacterID || receipt.MailID == "" {
			t.Errorf("callback boundary/receipt = committed:%v receipt:%+v", state.isCommitted(), receipt)
		}
	})
	if err != nil || !called {
		t.Fatalf("redelivery callback err=%v called=%v", err, called)
	}
}

func TestRedeliverCampaignRecipientCommittedSkipsCallbackWhenCommitFails(t *testing.T) {
	ctx := context.Background()
	group, campaign, character, _ := campaignCommitFixture(t)
	payload, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	store, state := openCommitBoundaryStore(t, payload)
	state.commitErr = errors.New("redelivery commit failed")
	recipient := Recipient{AccountID: character.AccountID, CharacterID: character.CharacterID, Name: character.Name, CreatedAt: character.CreatedAt}
	called := false
	_, err = store.RedeliverCampaignRecipientCommitted(ctx, group, campaign.ID, campaign.Revision, recipient, nil, func(context.Context, DeliveryReceipt) {
		called = true
	})
	if err == nil || called || state.isCommitted() {
		t.Fatalf("redelivery callback ran after failed commit: err=%v called=%v committed=%v", err, called, state.isCommitted())
	}
}
