package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func professionSkillHistory() repository.SkillRecord {
	v := repository.SkillRecord{CharacterID: "10", Skills: map[int64]repository.SkillState{}, Layouts: map[int]repository.SkillLayout{0: {}, 1: {}}, Cooldowns: map[int64]time.Time{}}
	for id := int64(1); id <= 180; id++ {
		v.Skills[id] = repository.SkillState{Level: 2, Enabled: true}
	}
	for i := 0; i < 80; i++ {
		v.Layouts[i%2][i] = uint16(i + 1)
	}
	for id := int64(1); id <= 40; id++ {
		v.Cooldowns[id] = time.Unix(2000000000+id, 0).UTC()
	}
	return v
}

func TestManagedProfessionSkillsPreserveUnchangedRows(t *testing.T) {
	before := professionSkillHistory()
	after := repository.CloneSkill(before)
	after.Skills[1] = repository.SkillState{Level: 3, Enabled: true}
	delete(after.Skills, 2)
	after.Skills[181] = repository.SkillState{Level: 1, Enabled: true}
	after.Layouts[0][0] = 181
	delete(after.Layouts[1], 1)
	delete(after.Cooldowns, 1)
	counts := 0
	for _, kind := range []string{mysqlSkillStatesTable, mysqlSkillLayoutsTable, mysqlSkillCooldownsTable} {
		keys, values := []string{"skill_id"}, []string{"skill_level", "enabled"}
		if kind == mysqlSkillLayoutsTable {
			keys, values = []string{"tree_id", "slot_index"}, []string{"skill_id"}
		}
		if kind == mysqlSkillCooldownsTable {
			values = []string{"expires_at"}
		}
		db := &fakeSQLDB{}
		if err := saveProfessionRows(context.Background(), db, kind, "10", keys, values, professionRows(before, kind), professionRows(after, kind)); err != nil {
			t.Fatal(err)
		}
		counts += len(db.execs)
		for _, call := range db.execs {
			if strings.HasPrefix(call.Query, "DELETE") && !strings.Contains(call.Query, " AND (") {
				t.Fatal("whole skill group deleted", call)
			}
			if len(call.Args) > 9 {
				t.Fatal("unchanged rows rewritten", call)
			}
		}
		unchanged := &fakeSQLDB{}
		if err := saveProfessionRows(context.Background(), unchanged, kind, "10", keys, values, professionRows(before, kind), professionRows(before, kind)); err != nil || len(unchanged.execs) != 0 {
			t.Fatal(err, unchanged.execs)
		}
	}
	if counts != 5 {
		t.Fatal("unexpected round trips", counts)
	}
}

type delayedProfessionDB struct{ fakeSQLDB }

func (d *delayedProfessionDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return d.fakeSQLDB.ExecContext(ctx, q, args...)
	}
}
func TestManagedProfessionSkillSaveWithinLatencyBudget(t *testing.T) {
	before := professionSkillHistory()
	after := repository.CloneSkill(before)
	after.Skills[1] = repository.SkillState{Level: 3, Enabled: true}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := replaceSkillStates(ctx, &delayedProfessionDB{}, "states", "10", after.Skills); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("old save did not reproduce timeout", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	db := &delayedProfessionDB{}
	if err := saveProfessionRows(ctx2, db, "states", "10", []string{"skill_id"}, []string{"skill_level", "enabled"}, professionRows(before, mysqlSkillStatesTable), professionRows(after, mysqlSkillStatesTable)); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != 1 {
		t.Fatal(len(db.execs))
	}
	t.Log("180 learned skills: 181 SQL writes reduced to 1 for one changed skill")
}
func TestManagedProfessionSkillResetBatchesAndErrors(t *testing.T) {
	before := professionSkillHistory()
	db := &fakeSQLDB{}
	if err := saveProfessionRows(context.Background(), db, "states", "10", []string{"skill_id"}, []string{"skill_level", "enabled"}, professionRows(before, mysqlSkillStatesTable), nil); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != 1 || !strings.HasPrefix(db.execs[0].Query, "DELETE") || len(db.execs[0].Args) != 181 {
		t.Fatal(db.execs)
	}
	rows := professionRows(before, mysqlSkillStatesTable)
	for i := int64(181); i <= 1024; i++ {
		before.Skills[i] = repository.SkillState{Level: 1}
	}
	db = &fakeSQLDB{}
	if err := saveProfessionRows(context.Background(), db, "states", "10", []string{"skill_id"}, []string{"skill_level", "enabled"}, rows, professionRows(before, mysqlSkillStatesTable)); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != 5 {
		t.Fatal("unbounded writes", len(db.execs))
	}
	for _, call := range db.execs {
		if len(call.Args) > 800 {
			t.Fatal("unbounded batch")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := saveProfessionRows(ctx, &delayedProfessionDB{}, "states", "10", []string{"skill_id"}, []string{"skill_level", "enabled"}, nil, rows); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
