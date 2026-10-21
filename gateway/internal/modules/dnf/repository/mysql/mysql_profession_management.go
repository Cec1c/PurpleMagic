package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const professionOperationsTable = "profession_management_operations"

var _ repository.ProfessionManagementReader = (*mysqlCharStore)(nil)
var _ repository.ProfessionManagementUnitOfWork = (*mysqlCharStore)(nil)

func professionQuestFilter(ids []int64) (string, []any, error) {
	if len(ids) > 4096 {
		return "", nil, repository.ErrResourceRange
	}
	args := make([]any, 0, len(ids))
	last := int64(0)
	for _, id := range ids {
		if id <= last {
			return "", nil, repository.ErrResourceRange
		}
		last = id
		args = append(args, id)
	}
	if len(ids) == 0 {
		return " AND 1=0", nil, nil
	}
	return " AND quest_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args, nil
}
func (s *mysqlCharStore) LoadProfessionManagement(ctx context.Context, id string, ids []int64) (repository.ProfessionManagementState, error) {
	snapshot, close, e := s.resourceSnapshot(ctx)
	if e != nil {
		return repository.ProfessionManagementState{}, e
	}
	defer close()
	return snapshot.loadProfessionManagement(ctx, id, ids)
}
func (s *mysqlCharStore) loadProfessionManagement(ctx context.Context, id string, ids []int64) (repository.ProfessionManagementState, error) {
	var v repository.ProfessionManagementState
	filter, args, e := professionQuestFilter(ids)
	if e != nil {
		return v, e
	}
	v.Growth, e = s.loadLevelManagement(ctx, id)
	if e != nil {
		return v, e
	}
	// Receipt recovery needs only the owner projection, even if the learned
	// skill or quest payload has subsequently become unavailable.
	if ids == nil {
		return v, nil
	}
	r := s.router
	// Bound full learned-skill rows before using the existing authoritative codec.
	for _, kind := range []string{mysqlSkillStatesTable, mysqlSkillLayoutsTable, mysqlSkillCooldownsTable} {
		t, e := r.readTable(kind, id)
		if e != nil {
			return v, e
		}
		var count int
		if e = r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t+" WHERE character_id=?", id).Scan(&count); e != nil {
			return v, e
		}
		if count > 4096 {
			return v, repository.ErrResourceRange
		}
	}
	k := mysqlSkillStore{mysqlStoreBase: mysqlStoreBase{router: r}}
	v.Growth.Skill, v.Growth.Found, e = k.Load(ctx, id)
	if e != nil {
		return v, e
	}
	v.Quests = repository.QuestRecord{CharacterID: id, States: map[int64]repository.QuestState{}, Progress: map[int64]repository.QuestState{}}
	parent, e := r.readTable(mysqlQuestTable, id)
	if e != nil {
		return v, e
	}
	var at sql.NullTime
	e = r.db.QueryRowContext(ctx, r.selectQuery("SELECT updated_at FROM "+parent+" WHERE character_id=?"), id).Scan(&at)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	v.Quests.UpdatedAt = scanTime(at)
	t, e := r.readTable(mysqlQuestStatesTable, id)
	if e != nil {
		return v, e
	}
	args = append([]any{id}, args...)
	rows, e := r.db.QueryContext(ctx, r.selectQuery("SELECT state_group,quest_id,status,trigger_type,progress_value,reward_select_index,multiplier,state_updated_at FROM "+t+" WHERE character_id=?"+filter+" AND state_group IN ('states','progress') ORDER BY state_group,quest_id"), args...)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var group string
		var qid int64
		var q repository.QuestState
		var at sql.NullTime
		if e = rows.Scan(&group, &qid, &q.Status, &q.TriggerType, &q.ProgressValue, &q.RewardSelectIndex, &q.Multiplier, &at); e != nil {
			return v, e
		}
		q.UpdatedAt = scanTime(at)
		if group == "states" {
			v.Quests.States[qid] = q
		} else {
			v.Quests.Progress[qid] = q
		}
	}
	return v, rows.Err()
}

type mysqlProfessionManagementStore struct {
	base               *mysqlCharStore
	account, character string
	allowed            map[int64]bool
	beforeSkills       *repository.SkillRecord
}

func (s *mysqlProfessionManagementStore) LoadProfessionManagement(ctx context.Context, id string, ids []int64) (repository.ProfessionManagementState, error) {
	if id != s.character {
		return repository.ProfessionManagementState{}, repository.ErrResourceRange
	}
	v, e := s.base.loadProfessionManagement(ctx, id, ids)
	if e != nil {
		return v, e
	}
	s.allowed = map[int64]bool{}
	for _, id := range ids {
		s.allowed[id] = true
	}
	if ids != nil {
		copy := repository.CloneSkill(v.Growth.Skill)
		s.beforeSkills = &copy
	}
	return v, nil
}
func (s *mysqlProfessionManagementStore) SaveProfessionManagement(ctx context.Context, v repository.ProfessionManagementState, changed []int64) (err error) {
	done := diagnostics.Start(ctx, "profession.save")
	defer func() { done(err) }()
	if s.beforeSkills == nil {
		return repository.ErrResourceRange
	}
	c := v.Growth.Character
	job, e := strconv.ParseUint(c.Job, 10, 8)
	if e != nil || strconv.FormatUint(job, 10) != c.Job || c.Stats["grow_type"] < 0 || c.Stats["grow_type"] > 255 || v.Quests.CharacterID != s.character || v.Quests.UpdatedAt.IsZero() {
		return repository.ErrResourceRange
	}
	if _, _, e = professionQuestFilter(changed); e != nil {
		return e
	}
	for _, id := range changed {
		if !s.allowed[id] {
			return repository.ErrResourceRange
		}
	}
	level := mysqlLevelManagementStore{base: s.base, account: s.account, character: s.character}
	if e = level.SaveLevelManagement(ctx, v.Growth); e != nil {
		return e
	}
	r := s.base.router
	ct, e := r.writeTable(mysqlCharTable, s.character)
	if e != nil {
		return e
	}
	if _, e = r.db.ExecContext(ctx, "UPDATE "+ct+" SET job=?,grow_type=? WHERE character_id=? AND BINARY account_id=BINARY ?", c.Job, c.Stats["grow_type"], s.character, s.account); e != nil {
		return e
	}
	if !characterMirrorStatsAuthoritative() {
		st, e := r.writeTable(mysqlCharacterStatsTable, s.character)
		if e != nil {
			return e
		}
		if _, e = r.db.ExecContext(ctx, "UPDATE "+st+" SET stat_value=? WHERE character_id=? AND stat_key='grow_type'", c.Stats["grow_type"], s.character); e != nil {
			return e
		}
	}
	if e = saveProfessionSkillDelta(ctx, r, s.character, *s.beforeSkills, v.Growth.Skill); e != nil {
		return e
	}
	if len(changed) == 0 {
		return nil
	}
	qt, e := r.writeTable(mysqlQuestTable, s.character)
	if e != nil {
		return e
	}
	st, e := r.writeTable(mysqlQuestStatesTable, s.character)
	if e != nil {
		return e
	}
	et, e := r.writeTable(mysqlQuestExtraTable, s.character)
	if e != nil {
		return e
	}
	if _, e = r.db.ExecContext(ctx, "INSERT INTO "+qt+"(character_id,updated_at) VALUES(?,?) ON DUPLICATE KEY UPDATE updated_at=VALUES(updated_at)", s.character, v.Quests.UpdatedAt); e != nil {
		return e
	}
	for _, id := range changed {
		for _, group := range []string{"states", "progress"} {
			m := v.Quests.States
			if group == "progress" {
				m = v.Quests.Progress
			}
			q, ok := m[id]
			if !ok {
				continue
			}
			// Only completion fields change. Reward metadata, counters, and extras
			// on existing rows retain their original storage values.
			if q.Status != "completed" || q.ProgressValue != 0 {
				return repository.ErrResourceRange
			}
			_, e = r.db.ExecContext(ctx, "INSERT INTO "+st+"(character_id,state_group,quest_id,status,progress_value,state_updated_at) VALUES(?,?,?,'completed',0,?) ON DUPLICATE KEY UPDATE status=VALUES(status),progress_value=VALUES(progress_value),state_updated_at=VALUES(state_updated_at)", s.character, group, id, q.UpdatedAt)
			if e != nil {
				return e
			}
			if q.Extra["gm_override"] == "gm_set_grow_type" {
				if _, e = r.db.ExecContext(ctx, "INSERT IGNORE INTO "+et+"(character_id,state_group,quest_id,extra_key,extra_value) VALUES(?,?,?,'gm_override','gm_set_grow_type')", s.character, group, id); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *mysqlCharStore) WithinProfessionManagement(ctx context.Context, account, id string, fn func(repository.ProfessionManagementRepository, repository.ManagementReceiptStore) error) error {
	if fn == nil {
		return repository.ErrResourceRange
	}
	return s.withinCharacterManagementRouter(ctx, account, id, professionOperationsTable, func(r mysqlRouter, receipts repository.ManagementReceiptStore) error {
		// All parents precede scalar subqueries and any consistent-read snapshot.
		for _, kind := range []string{mysqlSkillTable, mysqlQuestTable} {
			t, e := r.writeTable(kind, id)
			if e != nil {
				return e
			}
			var actual string
			e = r.db.QueryRowContext(ctx, "SELECT character_id FROM "+t+" WHERE character_id=? AND BINARY character_id=BINARY ? FOR UPDATE", id, id).Scan(&actual)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		return fn(&mysqlProfessionManagementStore{base: &mysqlCharStore{mysqlStoreBase: mysqlStoreBase{router: r}}, account: account, character: id}, receipts)
	})
}
func (s *mysqlCharStore) ProfessionManagementLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return s.characterManagementLog(ctx, id, before, limit, professionOperationsTable)
}
