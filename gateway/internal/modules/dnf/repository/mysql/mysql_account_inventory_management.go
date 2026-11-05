package mysql

import (
	"context"
	"database/sql"
	"errors"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const accountInventoryManagementTable = "account_inventory_management_operations"

type accountInventoryManagementReceipts struct{ accountManagementReceipts }

func (s accountInventoryManagementReceipts) Append(ctx context.Context, key string, raw []byte) error {
	if len(raw) > 2<<20 || len(key) < 16 || len(key) > 128 {
		return errors.New("invalid account inventory receipt")
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO "+s.table+" (account_id,request_id,payload,created_at) VALUES(?,?,?,UTC_TIMESTAMP(6))", s.account, key, raw)
	return err
}

type accountInventoryManagementCharacters struct {
	*characterSettlementScopedCharacterRepository
	account string
	reader  repository.CharacterSummaryReader
}

func (s accountInventoryManagementCharacters) ManagementAccountCharacters(ctx context.Context, account string) ([]repository.CharacterSummary, error) {
	if err := repository.TransactionKeyError(ctx, s.account, account); err != nil {
		return nil, err
	}
	return s.reader.SummariesForAccounts(ctx, []string{account})
}

// Account assets and the selected character can reside on different shards of
// one MySQL server. Lock the account first so two roles serialize on one owner.
func (s *mysqlAccountInventoryStore) WithinInventoryManagement(ctx context.Context, account, character string, apply func(repository.Group, repository.ManagementReceiptStore) error) error {
	return retryInventoryDeadlock(ctx, func() error { return s.withinInventoryManagement(ctx, account, character, apply) })
}

func (s *mysqlAccountInventoryStore) withinInventoryManagement(ctx context.Context, account, character string, apply func(repository.Group, repository.ManagementReceiptStore) error) error {
	if account == "" || character == "" || apply == nil {
		return errors.New("invalid account inventory operation")
	}
	beginner, ok := s.router.db.(sqlTxBeginner)
	if !ok {
		return errors.New("account inventory transaction unavailable")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r := s.router
	r.db = sqlTransactionDB{tx: tx}
	r.readDBs = append([]string(nil), r.writeDBs...)
	r.lockReads = true
	table, err := r.writeTable(mysqlAccountTable, account)
	if err != nil {
		return err
	}
	var actual string
	if err = tx.QueryRowContext(ctx, "SELECT account_id FROM "+table+" WHERE account_id=? AND BINARY account_id=BINARY ? FOR UPDATE", account, account).Scan(&actual); err != nil {
		return err
	}
	// Lock the inventory parent before the receipt lookup establishes a read
	// snapshot. Gameplay's account inventory transactions lock this same row.
	table, err = r.writeTable(mysqlAccountInventoryTable, account)
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, "SELECT account_id FROM "+table+" WHERE account_id=? AND BINARY account_id=BINARY ? FOR UPDATE", account, account).Scan(&actual)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	table, err = r.writeTable(mysqlCharTable, character)
	if err != nil {
		return err
	}
	// Ownership is a snapshot read. Account edits never write the selected role;
	// taking its X lock here reverses gameplay's character -> account order.
	// The account fence and occupancy checks serialize admission and deletion.
	if err = tx.QueryRowContext(ctx, "SELECT character_id FROM "+table+" WHERE character_id=? AND BINARY character_id=BINARY ? AND BINARY account_id=BINARY ?", character, character, account).Scan(&actual); err != nil {
		return err
	}
	base := newMySQLGroupFromRouter(r, false)
	readRouter := r
	readRouter.lockReads = false
	roles := newMySQLGroupFromRouter(readRouter, false).Character
	g := repository.Group{
		Character:        accountInventoryManagementCharacters{&characterSettlementScopedCharacterRepository{characterID: character, base: roles}, account, roles.(repository.CharacterSummaryReader)},
		AccountInventory: &accountInventoryScopedRepository{accountID: account, base: base.AccountInventory},
	}
	table, err = r.writeTable(accountInventoryManagementTable, account)
	if err != nil {
		return err
	}
	if err = apply(g, accountInventoryManagementReceipts{accountManagementReceipts{db: r.db, table: table, account: account}}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *mysqlAccountInventoryStore) InventoryManagementLog(ctx context.Context, account string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return readAccountManagementLog(ctx, s.router, accountInventoryManagementTable, account, before, limit)
}
