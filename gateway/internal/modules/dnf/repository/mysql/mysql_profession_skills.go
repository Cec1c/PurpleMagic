package mysql

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Used only under the profession management fence. The level writer already
// saved point totals; preserve unchanged skills, layouts and cooldown rows.
func saveProfessionSkillDelta(ctx context.Context, r mysqlRouter, character string, before, after repository.SkillRecord) (err error) {
	done := diagnostics.Start(ctx, "profession.skills_delta")
	defer func() { done(err) }()
	if before.CharacterID != character || after.CharacterID != character {
		return repository.ErrResourceRange
	}
	for _, kind := range []string{mysqlSkillStatesTable, mysqlSkillLayoutsTable, mysqlSkillCooldownsTable} {
		table, err := r.writeTable(kind, character)
		if err != nil {
			return err
		}
		keys, values := []string{"skill_id"}, []string{"skill_level", "enabled"}
		if kind == mysqlSkillLayoutsTable {
			keys, values = []string{"tree_id", "slot_index"}, []string{"skill_id"}
		}
		if kind == mysqlSkillCooldownsTable {
			values = []string{"expires_at"}
		}
		if err = saveProfessionRows(ctx, r.db, table, character, keys, values, professionRows(before, kind), professionRows(after, kind)); err != nil {
			return err
		}
	}
	return nil
}

type professionRow struct{ keys, values []any }

func professionRows(v repository.SkillRecord, kind string) map[string]professionRow {
	rows := map[string]professionRow{}
	switch kind {
	case mysqlSkillStatesTable:
		for id, state := range v.Skills {
			rows[strconv.FormatInt(id, 10)] = professionRow{[]any{id}, []any{state.Level, state.Enabled}}
		}
	case mysqlSkillLayoutsTable:
		for tree, layout := range v.Layouts {
			for slot, id := range layout {
				rows[strconv.Itoa(tree)+":"+strconv.Itoa(slot)] = professionRow{[]any{tree, slot}, []any{id}}
			}
		}
	case mysqlSkillCooldownsTable:
		for id, expires := range v.Cooldowns {
			rows[strconv.FormatInt(id, 10)] = professionRow{[]any{id}, []any{expires.UTC()}}
		}
	}
	return rows
}

func saveProfessionRows(ctx context.Context, db SQLDB, table, character string, keys, values []string, before, after map[string]professionRow) error {
	if len(before) > 4096 || len(after) > 4096 {
		return repository.ErrResourceRange
	}
	changed := map[string]bool{}
	for key, old := range before {
		if next, ok := after[key]; !ok || !reflect.DeepEqual(old, next) {
			changed[key] = true
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			changed[key] = true
		}
	}
	ids := make([]string, 0, len(changed))
	for key := range changed {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	const batch = 200
	for start := 0; start < len(ids); start += batch {
		selected := ids[start:min(start+batch, len(ids))]
		remove, insert := []string{}, []string{}
		removeArgs, insertArgs := []any{character}, []any{}
		for _, id := range selected {
			if row, ok := after[id]; ok {
				insert = append(insert, "("+strings.TrimSuffix(strings.Repeat("?,", 1+len(keys)+len(values)), ",")+")")
				insertArgs = append(insertArgs, character)
				insertArgs = append(insertArgs, row.keys...)
				insertArgs = append(insertArgs, row.values...)
			} else {
				parts := make([]string, len(keys))
				for i, k := range keys {
					parts[i] = k + "=?"
				}
				remove = append(remove, "("+strings.Join(parts, " AND ")+")")
				removeArgs = append(removeArgs, before[id].keys...)
			}
		}
		if len(remove) > 0 {
			if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE character_id=? AND ("+strings.Join(remove, " OR ")+")", removeArgs...); err != nil {
				return err
			}
		}
		if len(insert) > 0 {
			columns := append(append([]string{"character_id"}, keys...), values...)
			updates := make([]string, len(values))
			for i, v := range values {
				updates[i] = v + "=VALUES(" + v + ")"
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO "+table+"("+strings.Join(columns, ",")+") VALUES "+strings.Join(insert, ",")+" ON DUPLICATE KEY UPDATE "+strings.Join(updates, ","), insertArgs...); err != nil {
				return err
			}
		}
	}
	return nil
}
