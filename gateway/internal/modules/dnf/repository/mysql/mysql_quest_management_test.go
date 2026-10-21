package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func managedQuestHistory() repository.QuestRecord {
	r := repository.QuestRecord{CharacterID: "10", States: map[int64]repository.QuestState{}}
	for id := int64(1); id <= 769; id++ {
		extra := map[string]string{}
		count := 6
		if id <= 154 {
			count = 7
		}
		for n := 0; n < count; n++ {
			extra[fmt.Sprint(n)] = fmt.Sprint(id, n)
		}
		r.States[id] = repository.QuestState{Status: "completed", Extra: extra}
	}
	return r
}

func TestManagedQuestDeltaPreservesHistoryAndBoundsWrites(t *testing.T) {
	t.Setenv(currentMysqlInsertBatchEnv, "1")
	before := managedQuestHistory()
	after := repository.CloneQuest(before)
	v := after.States[1]
	v.Status = "active"
	v.Extra["new"] = "value"
	delete(v.Extra, "0")
	after.States[1] = v
	old := &fakeSQLDB{}
	if err := replaceQuestStates(context.Background(), old, "states", "extras", "10", "states", after.States); err != nil {
		t.Fatal(err)
	}
	db := &fakeSQLDB{}
	if err := saveManagedQuestDelta(context.Background(), db, "states", "extras", "10", "states", before.States, after.States); err != nil {
		t.Fatal(err)
	}
	if len(old.execs) != 5539 || len(db.execs) != 4 {
		t.Fatalf("SQL writes %d -> %d", len(old.execs), len(db.execs))
	}
	for _, call := range db.execs {
		if strings.HasPrefix(call.Query, "DELETE") && (!strings.Contains(call.Query, "quest_id IN (?)") || call.Args[2] != int64(1)) {
			t.Fatal("unrelated task deleted", call)
		}
	}
	t.Logf("769 states / 4768 extras: %d -> %d writes", len(old.execs), len(db.execs))
	db = &fakeSQLDB{}
	if err := saveManagedQuestDelta(context.Background(), db, "states", "extras", "10", "states", before.States, before.States); err != nil || len(db.execs) != 0 {
		t.Fatal("unchanged history rewritten", err)
	}
	delete(after.States, 1)
	db = &fakeSQLDB{}
	if err := saveManagedQuestDelta(context.Background(), db, "states", "extras", "10", "progress", before.States, after.States); err != nil || len(db.execs) != 2 {
		t.Fatal("reset", err, len(db.execs))
	}
}

type delayedQuestDB struct{ fakeSQLDB }

func (d *delayedQuestDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Millisecond):
		return d.fakeSQLDB.ExecContext(ctx, q, args...)
	}
}
func TestManagedQuestDeltaCompletesWithinLatencyBudget(t *testing.T) {
	t.Setenv(currentMysqlInsertBatchEnv, "1")
	before := managedQuestHistory()
	after := repository.CloneQuest(before)
	v := after.States[1]
	v.Status = "active"
	after.States[1] = v
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := replaceQuestStates(ctx, &delayedQuestDB{}, "states", "extras", "10", "states", after.States); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("legacy did not reproduce timeout", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if err := saveManagedQuestDelta(ctx2, &delayedQuestDB{}, "states", "extras", "10", "states", before.States, after.States); err != nil {
		t.Fatal(err)
	}
}

func TestManagedQuestDeltaBatchesLargeChanges(t *testing.T) {
	db := &fakeSQLDB{}
	r := managedQuestHistory()
	if err := saveManagedQuestDelta(context.Background(), db, "states", "extras", "10", "states", nil, r.States); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) > 40 {
		t.Fatal("unbounded round trips", len(db.execs))
	}
	for _, call := range db.execs {
		if strings.HasPrefix(call.Query, "DELETE") && len(call.Args) > 202 {
			t.Fatal("unbounded deletion")
		}
		if strings.HasPrefix(call.Query, "INSERT") && len(call.Args) > 200*9 {
			t.Fatal("unbounded insertion")
		}
	}
}
