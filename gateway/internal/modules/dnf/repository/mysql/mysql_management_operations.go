package mysql

import (
	"context"
	"database/sql"
	"errors"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const managementOperationsTable = "management_operations"

// EnsureManagementSchema only creates the additive management receipt tables.
func EnsureManagementSchema(ctx context.Context, db *sql.DB, options MySQLGroupOptions) error {
	r, err := newMySQLRouter(SQLDBAdapter{DB: db}, options)
	if err != nil {
		return err
	}
	for _, database := range r.writeDBs {
		for _, receiptTable := range []string{managementOperationsTable, accountInventoryManagementTable, accountVaultOperationsTable, adventureRenameOperationsTable} {
			table := mysqlTable(database, r.tablePrefix, receiptTable)
			_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+" (sequence_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, account_id VARBINARY(128) NOT NULL, request_id VARBINARY(128) NOT NULL, payload MEDIUMBLOB NOT NULL, created_at DATETIME(6) NOT NULL, UNIQUE KEY account_request(account_id,request_id), KEY account_sequence(account_id,sequence_id)) ENGINE=InnoDB")
			if err != nil {
				return err
			}
		}
		for _, receiptTable := range []string{characterRenameOperationsTable, "character_management_operations", "inventory_management_operations", "character_gold_operations", skillPointOperationsTable, levelOperationsTable, professionOperationsTable, expertJobOperationsTable, "container_management_operations", mailboxManagementTable, mailboxCleanupTable} {
			_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+mysqlTable(database, r.tablePrefix, receiptTable)+" (sequence_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, character_id VARBINARY(128) NOT NULL, account_id VARBINARY(128) NOT NULL, request_id VARBINARY(128) NOT NULL, payload MEDIUMBLOB NOT NULL, created_at DATETIME(6) NOT NULL, UNIQUE KEY character_request(character_id,request_id), KEY character_sequence(character_id,sequence_id)) ENGINE=InnoDB")
			if err != nil {
				return err
			}
		}
		_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+mysqlTable(database, r.tablePrefix, mailboxCleanupItemsTable)+" (character_id VARBINARY(128) NOT NULL,job_id VARBINARY(128) NOT NULL,ordinal BIGINT NOT NULL,payload MEDIUMBLOB NOT NULL,PRIMARY KEY(character_id,job_id,ordinal)) ENGINE=InnoDB")
		if err != nil {
			return err
		}
	}
	return nil
}

func CheckManagementSchema(ctx context.Context, db *sql.DB, options MySQLGroupOptions) error {
	r, err := newMySQLRouter(SQLDBAdapter{DB: db}, options)
	if err != nil {
		return err
	}
	for _, database := range r.writeDBs {
		for _, receiptTable := range []string{managementOperationsTable, accountInventoryManagementTable, accountVaultOperationsTable, adventureRenameOperationsTable} {
			rows, err := db.QueryContext(ctx, "SELECT sequence_id,account_id,request_id,payload,created_at FROM "+mysqlTable(database, r.tablePrefix, receiptTable)+" LIMIT 0")
			if err != nil {
				return err
			}
			rows.Close()
		}
		for _, receiptTable := range []string{characterRenameOperationsTable, "character_management_operations", "inventory_management_operations", "character_gold_operations", skillPointOperationsTable, levelOperationsTable, professionOperationsTable, expertJobOperationsTable, "container_management_operations", mailboxManagementTable, mailboxCleanupTable} {
			rows, err := db.QueryContext(ctx, "SELECT sequence_id,character_id,account_id,request_id,payload,created_at FROM "+mysqlTable(database, r.tablePrefix, receiptTable)+" LIMIT 0")
			if err != nil {
				return err
			}
			rows.Close()
		}
		rows, err := db.QueryContext(ctx, "SELECT character_id,job_id,ordinal,payload FROM "+mysqlTable(database, r.tablePrefix, mailboxCleanupItemsTable)+" LIMIT 0")
		if err != nil {
			return err
		}
		rows.Close()
	}
	return nil
}

type accountManagementReceipts struct {
	db             SQLDB
	table, account string
}

func (s accountManagementReceipts) Load(ctx context.Context, key string) ([]byte, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM "+s.table+" WHERE account_id=? AND request_id=?", s.account, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return raw, err == nil, err
}
func (s accountManagementReceipts) Append(ctx context.Context, key string, raw []byte) error {
	if len(raw) > 16384 || len(key) < 16 || len(key) > 128 {
		return errors.New("invalid management receipt")
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO "+s.table+" (account_id,request_id,payload,created_at) VALUES(?,?,?,UTC_TIMESTAMP(6))", s.account, key, raw)
	return err
}

func (s *mysqlAccountStore) WithinAccountManagement(ctx context.Context, id string, apply func(repository.AccountRepository, repository.ManagementReceiptStore) error) error {
	if apply == nil || id == "" {
		return errors.New("invalid account operation")
	}
	beginner, ok := s.router.db.(sqlTxBeginner)
	if !ok {
		return errors.New("account transaction unavailable")
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
	accountTable, err := r.writeTable(mysqlAccountTable, id)
	if err != nil {
		return err
	}
	var actual string
	err = tx.QueryRowContext(ctx, "SELECT account_id FROM "+accountTable+" WHERE account_id=? AND BINARY account_id=BINARY ? FOR UPDATE", id, id).Scan(&actual)
	if err != nil {
		return err
	}
	accounts := &accountCurrencyRepository{
		rentalScopedAccountRepository: rentalScopedAccountRepository{accountID: id, base: &mysqlAccountStore{mysqlStoreBase: mysqlStoreBase{router: r}}},
		router:                        r,
	}
	table, err := r.writeTable(managementOperationsTable, id)
	if err != nil {
		return err
	}
	if err = apply(accounts, accountManagementReceipts{db: r.db, table: table, account: id}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *mysqlAccountStore) AccountManagementLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return readAccountManagementLog(ctx, s.router, managementOperationsTable, id, before, limit)
}

func readAccountManagementLog(ctx context.Context, router mysqlRouter, receiptTable, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	if id == "" || limit < 1 || limit > 101 || before < 0 {
		return nil, errors.New("invalid management page")
	}
	table, err := router.readTable(receiptTable, id)
	if err != nil {
		return nil, err
	}
	q := "SELECT sequence_id,payload FROM " + table + " WHERE account_id=?"
	args := []any{id}
	if before > 0 {
		q += " AND sequence_id<?"
		args = append(args, before)
	}
	q += " ORDER BY sequence_id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := router.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []repository.ManagementLogRow{}
	for rows.Next() {
		var row repository.ManagementLogRow
		if err = rows.Scan(&row.Sequence, &row.Payload); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
