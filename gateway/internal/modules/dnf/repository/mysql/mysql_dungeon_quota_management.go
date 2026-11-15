package mysql

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const dungeonQuotaOperationsTable = "dungeon_quota_management_operations"

var _ repository.DungeonQuotaManagementReader = (*mysqlCharStore)(nil)
var _ repository.DungeonQuotaManagementUnitOfWork = (*mysqlCharStore)(nil)

func validDungeonQuotaDeleteKey(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	return strings.HasPrefix(key, "dungeon_daily_entry_") ||
		strings.HasPrefix(key, "dungeon_cycle_entry_") ||
		strings.HasPrefix(key, "raid_weekly_epoch_v1_") ||
		strings.HasPrefix(key, "raid_weekly_clears_v1_")
}

func (s *mysqlCharStore) LoadDungeonQuotaManagement(ctx context.Context, id string) (repository.DungeonQuotaManagementState, error) {
	snapshot, closeFn, err := s.resourceSnapshot(ctx)
	if err != nil {
		return repository.DungeonQuotaManagementState{}, err
	}
	defer closeFn()
	return snapshot.loadDungeonQuotaManagement(ctx, id)
}

func (s *mysqlCharStore) loadDungeonQuotaManagement(ctx context.Context, id string) (repository.DungeonQuotaManagementState, error) {
	var v repository.DungeonQuotaManagementState
	if id == "" || len(id) > 128 {
		return v, repository.ErrResourceRange
	}
	r := s.router
	ct, err := r.readTable(mysqlCharTable, id)
	if err != nil {
		return v, err
	}
	st, err := r.readTable(mysqlCharacterStatsTable, id)
	if err != nil {
		return v, err
	}
	deleted := "c.delete_flag"
	if !characterMirrorStatsAuthoritative() {
		deleted = "COALESCE((SELECT stat_value FROM " + st + " WHERE character_id=c.character_id AND stat_key='delete_flag'),c.delete_flag)"
	}
	var deleteFlag int64
	var updated sql.NullTime
	err = r.db.QueryRowContext(
		ctx,
		r.selectQuery("SELECT c.character_id,c.account_id,c.level,"+deleted+",c.updated_at FROM "+ct+" c WHERE c.character_id=? AND BINARY c.character_id=BINARY ?"),
		id, id,
	).Scan(&v.Character.CharacterID, &v.Character.AccountID, &v.Character.Level, &deleteFlag, &updated)
	if err != nil {
		return v, err
	}
	v.Character.UpdatedAt = scanTime(updated)
	stats := map[string]int64{
		"delete_flag":                deleteFlag,
		"gm_dungeon_quota_sequence_v1": 0,
	}
	rows, err := r.db.QueryContext(
		ctx,
		r.selectQuery("SELECT stat_key,stat_value FROM "+st+" WHERE character_id=? AND (stat_key LIKE 'dungeon_daily_entry_%' OR stat_key LIKE 'dungeon_cycle_entry_%' OR stat_key LIKE 'raid_weekly_%' OR stat_key='gm_dungeon_quota_sequence_v1')"),
		id,
	)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var val int64
		if err = rows.Scan(&key, &val); err != nil {
			return v, err
		}
		stats[key] = val
	}
	if err = rows.Err(); err != nil {
		return v, err
	}
	v.Character.Stats = stats
	return v, nil
}

type mysqlDungeonQuotaManagementStore struct {
	base               *mysqlCharStore
	account, character string
}

func (s *mysqlDungeonQuotaManagementStore) LoadDungeonQuotaManagement(ctx context.Context, id string) (repository.DungeonQuotaManagementState, error) {
	if id != s.character {
		return repository.DungeonQuotaManagementState{}, repository.ErrResourceRange
	}
	return s.base.loadDungeonQuotaManagement(ctx, id)
}

func (s *mysqlDungeonQuotaManagementStore) ResetDungeonQuotaKeys(ctx context.Context, characterID string, deleteKeys []string, sequence int64, updatedAt time.Time) error {
	if characterID != s.character || s.account == "" || sequence <= 0 || updatedAt.IsZero() || len(deleteKeys) == 0 || len(deleteKeys) > 64 {
		return repository.ErrResourceRange
	}
	seen := make(map[string]bool, len(deleteKeys))
	args := make([]any, 0, len(deleteKeys)+1)
	args = append(args, s.character)
	for _, key := range deleteKeys {
		if !validDungeonQuotaDeleteKey(key) || seen[key] {
			return repository.ErrResourceRange
		}
		seen[key] = true
		args = append(args, key)
	}
	r := s.base.router
	ct, err := r.writeTable(mysqlCharTable, s.character)
	if err != nil {
		return err
	}
	st, err := r.writeTable(mysqlCharacterStatsTable, s.character)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(
		ctx,
		"UPDATE "+ct+" SET updated_at=? WHERE character_id=? AND BINARY character_id=BINARY ? AND BINARY account_id=BINARY ? AND delete_flag=0",
		updatedAt.UTC(), s.character, s.character, s.account,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return repository.ErrResourceRange
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(deleteKeys)), ",")
	if _, err = r.db.ExecContext(ctx, "DELETE FROM "+st+" WHERE character_id=? AND stat_key IN ("+placeholders+")", args...); err != nil {
		return err
	}
	_, err = r.db.ExecContext(
		ctx,
		"INSERT INTO "+st+"(character_id,stat_key,stat_value) VALUES(?,'gm_dungeon_quota_sequence_v1',?) ON DUPLICATE KEY UPDATE stat_value=VALUES(stat_value)",
		s.character, sequence,
	)
	return err
}

func (s *mysqlCharStore) WithinDungeonQuotaManagement(ctx context.Context, account, id string, fn func(repository.DungeonQuotaManagementRepository, repository.ManagementReceiptStore) error) error {
	if fn == nil {
		return repository.ErrResourceRange
	}
	return s.withinCharacterManagementRouter(ctx, account, id, dungeonQuotaOperationsTable, func(r mysqlRouter, receipts repository.ManagementReceiptStore) error {
		return fn(&mysqlDungeonQuotaManagementStore{
			base:      &mysqlCharStore{mysqlStoreBase: mysqlStoreBase{router: r}},
			account:   account,
			character: id,
		}, receipts)
	})
}

func (s *mysqlCharStore) DungeonQuotaManagementLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return s.characterManagementLog(ctx, id, before, limit, dungeonQuotaOperationsTable)
}
