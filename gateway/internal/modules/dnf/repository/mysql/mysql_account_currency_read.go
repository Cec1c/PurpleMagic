package mysql

import (
	"context"
	"database/sql"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *mysqlAccountStore) LoadAccountCurrency(ctx context.Context, id string) (repository.AccountRecord, bool, error) {
	accounts, err := s.router.readTable(mysqlAccountTable, id)
	if err != nil {
		return repository.AccountRecord{}, false, err
	}
	metadata, err := s.router.readTable(mysqlAccountMetadataTable, id)
	if err != nil {
		return repository.AccountRecord{}, false, err
	}
	var v repository.AccountRecord
	var balance, sequence sql.NullString
	err = diagnosticRow(ctx, s.router.db, "account.currency", "SELECT a.account_id,b.entry_value,v.entry_value FROM "+accounts+" a LEFT JOIN "+metadata+" b ON b.account_id=a.account_id AND b.entry_key='account_cera' LEFT JOIN "+metadata+" v ON v.account_id=a.account_id AND v.entry_key='gm_currency_sequence_v1' WHERE a.account_id=? AND BINARY a.account_id=BINARY ?", id, id).Scan(&v.AccountID, &balance, &sequence)
	if err != nil {
		found, e := scanErr(err)
		return v, found, e
	}
	v.Metadata = map[string]string{}
	if balance.Valid {
		v.Metadata["account_cera"] = balance.String
	}
	if sequence.Valid {
		v.Metadata["gm_currency_sequence_v1"] = sequence.String
	}
	return v, true, nil
}
