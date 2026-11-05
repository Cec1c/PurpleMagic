package mysql

import (
	"context"
	"database/sql"
	"errors"
)

// Receipt recovery must not acquire live character/inventory locks. The insert
// is atomic with the edit, and writers still recheck it inside their transaction.
func loadInventoryManagementReceipt(ctx context.Context, r mysqlRouter, table, ownerColumn, owner, key string) ([]byte, bool, error) {
	t, err := r.writeTable(table, owner)
	if err != nil {
		return nil, false, err
	}
	var raw []byte
	err = r.db.QueryRowContext(ctx, "SELECT payload FROM "+t+" WHERE "+ownerColumn+"=? AND request_id=?", owner, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func (s *mysqlAccountInventoryStore) LookupInventoryReceipt(ctx context.Context, account, character, key string) ([]byte, bool, error) {
	return loadInventoryManagementReceipt(ctx, s.router, accountInventoryManagementTable, "account_id", account, key)
}

func (s *mysqlCharStore) LookupInventoryReceipt(ctx context.Context, account, character, key string) ([]byte, bool, error) {
	return loadInventoryManagementReceipt(ctx, s.router, "inventory_management_operations", "character_id", character, key)
}
