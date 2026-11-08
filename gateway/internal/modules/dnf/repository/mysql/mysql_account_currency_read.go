package mysql

import (
	"context"
	"database/sql"
	"strings"

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

func (s *mysqlAccountStore) LoadAccountCurrencies(ctx context.Context, ids []string) (map[string]repository.AccountRecord, error) {
	if len(ids) > 100 {
		return nil, repository.ErrResourceRange
	}
	type batch struct {
		accounts, metadata string
		ids                []string
	}
	groups := map[string]*batch{}
	allowed := map[string]bool{}
	for _, id := range ids {
		if allowed[id] {
			continue
		}
		allowed[id] = true
		accounts, err := s.router.readTable(mysqlAccountTable, id)
		if err != nil {
			return nil, err
		}
		metadata, err := s.router.readTable(mysqlAccountMetadataTable, id)
		if err != nil {
			return nil, err
		}
		key := accounts + ":" + metadata
		if groups[key] == nil {
			groups[key] = &batch{accounts: accounts, metadata: metadata}
		}
		groups[key].ids = append(groups[key].ids, id)
	}
	result := map[string]repository.AccountRecord{}
	for _, b := range groups {
		args := make([]any, len(b.ids))
		for i, id := range b.ids {
			args[i] = id
		}
		// Keep the indexed column unwrapped; the exact-case allowlist below
		// excludes collation-equivalent IDs that were not requested.
		query := "SELECT a.account_id,b.entry_value,v.entry_value FROM " + b.accounts + " a LEFT JOIN " + b.metadata + " b ON b.account_id=a.account_id AND b.entry_key='account_cera' LEFT JOIN " + b.metadata + " v ON v.account_id=a.account_id AND v.entry_key='gm_currency_sequence_v1' WHERE a.account_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
		rows, err := s.router.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a repository.AccountRecord
			var balance, sequence sql.NullString
			if err = rows.Scan(&a.AccountID, &balance, &sequence); err != nil {
				rows.Close()
				return nil, err
			}
			if !allowed[a.AccountID] {
				continue
			}
			a.Metadata = map[string]string{}
			if balance.Valid {
				a.Metadata["account_cera"] = balance.String
			}
			if sequence.Valid {
				a.Metadata["gm_currency_sequence_v1"] = sequence.String
			}
			result[a.AccountID] = a
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
