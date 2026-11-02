package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type currencyCanceledReceipts struct {
	repository.ManagementReceiptStore
	cancel context.CancelFunc
}

func (r currencyCanceledReceipts) Append(ctx context.Context, _ string, _ []byte) error {
	r.cancel()
	return ctx.Err()
}

type currencyCanceledUOW struct {
	repository.AccountManagementUnitOfWork
	cancel context.CancelFunc
}

func (u currencyCanceledUOW) WithinAccountManagement(ctx context.Context, id string, fn func(repository.AccountRepository, repository.ManagementReceiptStore) error) error {
	return u.AccountManagementUnitOfWork.WithinAccountManagement(ctx, id, func(a repository.AccountRepository, r repository.ManagementReceiptStore) error {
		return fn(a, currencyCanceledReceipts{r, u.cancel})
	})
}

func TestV1CurrencyScopedMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, u, open, databases := currencyFixtureDatabases(t, shards)
			ctx := context.Background()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			load := func() repository.AccountRecord {
				a, ok, err := open().Account.Load(ctx, "wallet:1")
				must(err)
				if !ok {
					t.Fatal("account missing")
				}
				return a
			}
			a := load()
			a.HonorExp = 12345
			a.RepresentAccountName = "fixture-owner"
			a.Metadata["gm_currency_sequence_v1"] = "0"
			a.Metadata["test_benefits_receipt_fixture"] = `{"delivered":true}`
			for i := len(a.Metadata); i < 268; i++ {
				a.Metadata[fmt.Sprintf("unrelated_%03d", i)] = fmt.Sprintf("preserve_%d", i)
			}
			must(open().Account.Save(ctx, a))
			before := load()
			if len(before.Metadata) != 268 {
				t.Fatal(len(before.Metadata))
			}
			// These are fixture-only guards. They detect delete/reinsert even if
			// the final values happen to look unchanged after an aggregate Save.
			for _, db := range databases {
				prefix := "`" + db + "`."
				for _, ddl := range []string{
					"CREATE TABLE " + prefix + "currency_touches(entry_key VARCHAR(128)) ENGINE=InnoDB",
					"CREATE TRIGGER " + prefix + "currency_no_delete BEFORE DELETE ON " + prefix + "dnf_account_metadata FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='currency must not delete metadata'",
					"CREATE TRIGGER " + prefix + "currency_only_insert BEFORE INSERT ON " + prefix + "dnf_account_metadata FOR EACH ROW BEGIN IF NEW.entry_key NOT IN ('account_cera','gm_currency_sequence_v1') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='unrelated metadata insert'; END IF; END",
					"CREATE TRIGGER " + prefix + "currency_only_update BEFORE UPDATE ON " + prefix + "dnf_account_metadata FOR EACH ROW BEGIN IF NEW.entry_key NOT IN ('account_cera','gm_currency_sequence_v1') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='unrelated metadata update'; END IF; END",
					"CREATE TRIGGER " + prefix + "currency_count_insert AFTER INSERT ON " + prefix + "dnf_account_metadata FOR EACH ROW INSERT INTO " + prefix + "currency_touches VALUES(NEW.entry_key)",
					"CREATE TRIGGER " + prefix + "currency_count_update AFTER UPDATE ON " + prefix + "dnf_account_metadata FOR EACH ROW INSERT INTO " + prefix + "currency_touches VALUES(NEW.entry_key)",
				} {
					_, err := s.db.Exec(ddl)
					must(err)
				}
			}
			touches := func() int {
				total := 0
				for _, db := range databases {
					var n int
					must(s.db.QueryRow("SELECT COUNT(*) FROM `" + db + "`.currency_touches").Scan(&n))
					total += n
				}
				return total
			}
			cmd := adminops.CurrencyCommand{AccountID: a.AccountID, Currency: "cera", Amount: "250", Revision: adminops.CurrencyRevision(before), Reason: "fixture", Actor: "fixture", Key: "currency-scoped-many-001"}
			start := time.Now()
			r, err := adminops.AdjustCurrency(ctx, u, cmd)
			must(err)
			t.Logf("268 metadata entries, %d shard(s): adjustment %s; exactly two wallet rows touched", shards, time.Since(start))
			if r.After != "1250" || touches() != 2 {
				t.Fatal(r, touches())
			}
			after := load()
			want := repository.CloneAccount(before)
			want.Metadata["account_cera"] = "1250"
			want.Metadata["gm_currency_sequence_v1"] = "1"
			want.UpdatedAt = after.UpdatedAt
			if !reflect.DeepEqual(after, want) {
				t.Fatal("non-wallet account data changed")
			}
			// A lost response can be recovered through the original request
			// without writing the wallet again, even with its old revision.
			again, err := adminops.AdjustCurrency(ctx, open().Account.(repository.AccountManagementUnitOfWork), cmd)
			must(err)
			if !reflect.DeepEqual(again, r) || touches() != 2 {
				t.Fatal("replay wrote wallet", again, touches())
			}
			cmd.Key = "currency-scoped-cancel-001"
			cmd.Revision = adminops.CurrencyRevision(after)
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			_, err = adminops.AdjustCurrency(cancelCtx, currencyCanceledUOW{u, cancel}, cmd)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("expected cancellation after wallet write", err)
			}
			if !reflect.DeepEqual(load(), after) || touches() != 2 {
				t.Fatal("canceled wallet transaction was not rolled back")
			}
			rows, err := u.AccountManagementLog(ctx, cmd.AccountID, 0, 100)
			must(err)
			if len(rows) != 1 {
				t.Fatal("canceled operation left a receipt", rows)
			}
			_, err = adminops.AdjustCurrency(ctx, u, cmd)
			must(err)
			if touches() != 4 || load().Metadata["account_cera"] != "1500" {
				t.Fatal("same request did not recover after rollback")
			}
		})
	}
}

type currencyDeadlineUOW struct{ repository.AccountRepository }

func (currencyDeadlineUOW) WithinAccountManagement(context.Context, string, func(repository.AccountRepository, repository.ManagementReceiptStore) error) error {
	return context.DeadlineExceeded
}
func (currencyDeadlineUOW) AccountManagementLog(context.Context, string, int64, int) ([]repository.ManagementLogRow, error) {
	return nil, nil
}

func TestV1CurrencyTimeoutMySQL(t *testing.T) {
	s, _, open := currencyFixture(t, 1)
	g := open()
	g.Account = currencyDeadlineUOW{g.Account}
	s.deps.Repos = func() (repository.Group, bool) { return g, true }
	token, _ := s.startSession()
	payload, _ := json.Marshal(map[string]string{"currency": "cera", "amount": "100", "revision": strings.Repeat("a", 64), "reason": "fixture"})
	status, body := operationCall(s, token, "accounts/wallet:1/currency-adjustments", string(payload), "currency-timeout-001", nil)
	id, _ := body["requestId"].(string)
	if status != 503 || body["error"].(map[string]any)["code"] != "currency_timeout" || body["error"].(map[string]any)["retryable"] != true || id == "" {
		t.Fatal(status, body)
	}
}
