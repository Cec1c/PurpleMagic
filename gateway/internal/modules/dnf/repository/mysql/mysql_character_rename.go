package mysql

import (
	"context"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type characterRenameStore struct {
	*characterSettlementScopedCharacterRepository
	router mysqlRouter
}

func (s *characterRenameStore) CheckCharacterName(ctx context.Context, name string) error {
	// Lock the matching name range in every shard. Concurrent renames and inserts
	// must finish before this transaction can claim the same name.
	for _, database := range s.router.writeDBs {
		rows, err := s.router.db.QueryContext(ctx, "SELECT character_id FROM "+mysqlTable(database, s.router.tablePrefix, mysqlCharTable)+" WHERE name=? AND delete_flag=0 FOR UPDATE", name)
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
			if id != s.characterID {
				taken = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if taken {
			return repository.ErrCharacterNameTaken
		}
	}
	return nil
}

func (s *characterRenameStore) RenameCharacter(ctx context.Context, name string, at time.Time) error {
	table, err := s.router.writeTable(mysqlCharTable, s.characterID)
	if err != nil {
		return err
	}
	_, err = s.router.db.ExecContext(ctx, "UPDATE "+table+" SET name=?,updated_at=? WHERE character_id=? AND delete_flag=0", name, at, s.characterID)
	return err
}

const characterRenameOperationsTable = "character_rename_operations"

func (s *mysqlCharStore) WithinCharacterRename(ctx context.Context, account, id string, apply func(repository.CharacterRenameRepository, repository.ManagementReceiptStore) error) error {
	return s.withinCharacterManagementRouter(ctx, account, id, characterRenameOperationsTable, func(r mysqlRouter, receipts repository.ManagementReceiptStore) error {
		base := newMySQLGroupFromRouter(r, false)
		return apply(&characterRenameStore{&characterSettlementScopedCharacterRepository{characterID: id, base: base.Character}, r}, receipts)
	})
}
func (s *mysqlCharStore) CharacterRenameLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return s.characterManagementLog(ctx, id, before, limit, characterRenameOperationsTable)
}
