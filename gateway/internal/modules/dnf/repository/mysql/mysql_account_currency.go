package mysql

import (
	"context"
	"errors"
	"strconv"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Only constructed after the account row has been locked in
// WithinAccountManagement; router.db is the same transaction as the receipt.
type accountCurrencyRepository struct {
	rentalScopedAccountRepository
	router mysqlRouter
}

var _ repository.AccountCurrencyWriter = (*accountCurrencyRepository)(nil)

func (s *accountCurrencyRepository) SaveMetadataEntry(ctx context.Context, id, key, value string, at time.Time) error {
	if err := repository.TransactionKeyError(ctx, s.accountID, id); err != nil {
		return err
	}
	return s.base.(repository.AccountMetadataEntryWriter).SaveMetadataEntry(ctx, id, key, value, at)
}

func (s *accountCurrencyRepository) SaveAccountCurrency(ctx context.Context, a repository.AccountRecord) error {
	if err := repository.TransactionKeyError(ctx, s.accountID, a.AccountID); err != nil {
		return err
	}
	for key, minimum := range map[string]int64{"account_cera": 0, "gm_currency_sequence_v1": 1} {
		raw := a.Metadata[key]
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < minimum || strconv.FormatInt(n, 10) != raw {
			return errors.New("invalid account currency value")
		}
	}
	if a.UpdatedAt.IsZero() {
		return errors.New("missing account currency timestamp")
	}
	account, err := s.router.writeTable(mysqlAccountTable, s.accountID)
	if err != nil {
		return err
	}
	metadata, err := s.router.writeTable(mysqlAccountMetadataTable, s.accountID)
	if err != nil {
		return err
	}
	if _, err = s.router.db.ExecContext(ctx, "UPDATE "+account+" SET updated_at=? WHERE account_id=? AND BINARY account_id=BINARY ?", a.UpdatedAt.UTC(), s.accountID, s.accountID); err != nil {
		return err
	}
	// One bounded statement for the two wallet keys. In particular, do not call
	// Account.Save/replaceStringMap: unrelated metadata and receipts stay intact.
	_, err = s.router.db.ExecContext(ctx, "INSERT INTO "+metadata+" (account_id,entry_key,entry_value) VALUES (?,?,?),(?,?,?) ON DUPLICATE KEY UPDATE entry_value=VALUES(entry_value)",
		s.accountID, "account_cera", a.Metadata["account_cera"],
		s.accountID, "gm_currency_sequence_v1", a.Metadata["gm_currency_sequence_v1"])
	return err
}
