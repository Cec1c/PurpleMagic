package mysql

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Only constructed inside the fenced management transaction. A load supplies
// the comparison snapshot; gameplay's aggregate SaveFields contract is unchanged.
type managedQuestStore struct {
	base      *mysqlQuestStore
	character string
	before    *repository.QuestRecord
}

func (s *managedQuestStore) Load(ctx context.Context, id string) (repository.QuestRecord, bool, error) {
	if err := repository.TransactionKeyError(ctx, s.character, id); err != nil {
		return repository.QuestRecord{}, false, err
	}
	r, found, err := s.base.Load(ctx, id)
	if err == nil {
		if !found {
			r = repository.QuestRecord{CharacterID: id}
		}
		copy := repository.CloneQuest(r)
		s.before = &copy
	}
	return r, found, err
}

func (s *managedQuestStore) Save(ctx context.Context, r repository.QuestRecord) error {
	return s.SaveFields(ctx, r, repository.AllQuestFields()...)
}

func (s *managedQuestStore) SaveFields(ctx context.Context, r repository.QuestRecord, fields ...repository.QuestField) (err error) {
	done := diagnostics.Start(ctx, "quest.save_delta")
	defer func() { done(err) }()
	if err = repository.TransactionKeyError(ctx, s.character, r.CharacterID); err != nil {
		return err
	}
	if s.before == nil {
		return errors.New("managed quest write requires a loaded snapshot")
	}
	fields = repository.QuestFields.Normalize(fields)
	if len(fields) == 0 {
		return nil
	}
	router := s.base.router
	table, err := router.writeTable(mysqlQuestTable, r.CharacterID)
	if err != nil {
		return err
	}
	states, err := router.writeTable(mysqlQuestStatesTable, r.CharacterID)
	if err != nil {
		return err
	}
	extras, err := router.writeTable(mysqlQuestExtraTable, r.CharacterID)
	if err != nil {
		return err
	}
	_, err = router.db.ExecContext(ctx, buildUpsert(table, []string{"character_id", "updated_at"}, []string{updateValue("updated_at")}), r.CharacterID, timeOrNow(r.UpdatedAt, router.now))
	if err != nil {
		return err
	}
	for _, field := range fields {
		var before, after map[int64]repository.QuestState
		switch field {
		case repository.QuestFieldStates:
			before, after = s.before.States, r.States
		case repository.QuestFieldProgress:
			before, after = s.before.Progress, r.Progress
		}
		if err = saveManagedQuestDelta(ctx, router.db, states, extras, r.CharacterID, string(field), before, after); err != nil {
			return err
		}
	}
	// Retain the loaded values for fields not included in this save.
	copy := repository.CloneQuest(r)
	for _, field := range fields {
		switch field {
		case repository.QuestFieldStates:
			s.before.States = copy.States
		case repository.QuestFieldProgress:
			s.before.Progress = copy.Progress
		}
	}
	return nil
}

func saveManagedQuestDelta(ctx context.Context, db SQLDB, statesTable, extrasTable, character, group string, before, after map[int64]repository.QuestState) error {
	changed := map[int64]bool{}
	for id, old := range before {
		next, exists := after[id]
		if !exists || !reflect.DeepEqual(old, next) {
			changed[id] = true
		}
	}
	for id := range after {
		if _, exists := before[id]; !exists {
			changed[id] = true
		}
	}
	ids := make([]int64, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	const batch = 200
	for start := 0; start < len(ids); start += batch {
		end := min(start+batch, len(ids))
		selected := ids[start:end]
		args := []any{character, group}
		next := map[int64]repository.QuestState{}
		for _, id := range selected {
			args = append(args, id)
			if v, exists := after[id]; exists {
				next[id] = v
			}
		}
		where := " WHERE character_id=? AND state_group=? AND quest_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(selected)), ",") + ")"
		for _, table := range []string{extrasTable, statesTable} {
			if _, err := db.ExecContext(ctx, "DELETE FROM "+table+where, args...); err != nil {
				return err
			}
		}
		if err := insertQuestStates(ctx, db, statesTable, extrasTable, character, group, next, batch); err != nil {
			return err
		}
	}
	return nil
}
