package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	driver "github.com/go-sql-driver/mysql"
)

const adventureRenameOperationsTable = "adventure_rename_operations"

func loadAdventureName(ctx context.Context, r mysqlRouter, id string) (repository.AdventureName, error) {
	var state repository.AdventureName
	table, err := r.readTable(mysqlAccountTable, id)
	if err != nil {
		return state, err
	}
	var name sql.NullString
	err = r.db.QueryRowContext(ctx, r.selectQuery("SELECT account_id,represent_account_name FROM "+table+" WHERE account_id=? AND BINARY account_id=BINARY ?"), id, id).Scan(&state.AccountID, &name)
	state.Name = name.String
	return state, err
}

func (s *mysqlAccountStore) LoadAdventureName(ctx context.Context, id string) (repository.AdventureName, error) {
	return loadAdventureName(ctx, s.router, id)
}

type adventureRenameStore struct {
	router  mysqlRouter
	account string
}

func (s *adventureRenameStore) LoadAdventureName(ctx context.Context, id string) (repository.AdventureName, error) {
	if e := repository.TransactionKeyError(ctx, s.account, id); e != nil {
		return repository.AdventureName{}, e
	}
	return loadAdventureName(ctx, s.router, id)
}

func (s *adventureRenameStore) CheckAdventureName(ctx context.Context, name string) error {
	// Use the database collation and lock the matching name range in all write
	// shards, including empty ranges, for competing rename claims.
	for _, database := range s.router.writeDBs {
		rows, err := s.router.db.QueryContext(ctx, "SELECT account_id FROM "+mysqlTable(database, s.router.tablePrefix, mysqlAccountTable)+" WHERE represent_account_name=? FOR UPDATE", name)
		if err != nil {
			return err
		}
		taken := false
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if id != s.account {
				taken = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if taken {
			return repository.ErrRepresentAccountNameExists
		}
	}
	return nil
}

func (s *adventureRenameStore) RenameAdventure(ctx context.Context, name string, at time.Time) error {
	table, err := s.router.writeTable(mysqlAccountTable, s.account)
	if err != nil {
		return err
	}
	_, err = s.router.db.ExecContext(ctx, "UPDATE "+table+" SET represent_account_name=?,updated_at=? WHERE account_id=? AND BINARY account_id=BINARY ?", name, at, s.account, s.account)
	var duplicate *driver.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		return repository.ErrRepresentAccountNameExists
	}
	return err
}

func (s *mysqlAccountStore) WithinAdventureRename(ctx context.Context, id string, fn func(repository.AdventureRenameRepository, repository.ManagementReceiptStore) error) error {
	if id == "" || fn == nil {
		return repository.ErrResourceRange
	}
	beginner, ok := s.router.db.(sqlTxBeginner)
	if !ok {
		return errors.New("adventure rename transaction unavailable")
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
	store := &adventureRenameStore{router: r, account: id}
	// This row is also the game's login admission fence.
	if _, err = store.LoadAdventureName(ctx, id); err != nil {
		return err
	}
	table, err := r.writeTable(adventureRenameOperationsTable, id)
	if err != nil {
		return err
	}
	if err = fn(store, accountManagementReceipts{db: r.db, table: table, account: id}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *mysqlAccountStore) AdventureRenameLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return readAccountManagementLog(ctx, s.router, adventureRenameOperationsTable, id, before, limit)
}
