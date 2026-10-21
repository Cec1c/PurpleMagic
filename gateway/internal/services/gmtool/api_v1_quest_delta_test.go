package gmtool

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1QuestTimeoutRetainsRetryableReceiptRecovery(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).v1QuestOperationResult(w, httptest.NewRequest("POST", "/api/v1/characters/10/quest-operations", nil), adminops.QuestReceipt{}, context.DeadlineExceeded)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"quest_operation_timeout"`) || !strings.Contains(w.Body.String(), `"retryable":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestV1QuestLargeHistoryDeltaMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			_, _, open := currencyFixture(t, shards)
			ctx := context.Background()
			g := open()
			c := repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "任务隔离测试", Level: 90, Job: "0", Stats: map[string]int64{"gold": 12345}}
			if err := g.Character.Save(ctx, c); err != nil {
				t.Fatal(err)
			}
			r := repository.QuestRecord{CharacterID: "10", States: map[int64]repository.QuestState{}, Progress: map[int64]repository.QuestState{900: {Status: "active", Extra: map[string]string{"opaque": "preserve"}}}}
			for id := int64(1); id <= 769; id++ {
				extra := map[string]string{}
				count := 6
				if id <= 154 {
					count = 7
				}
				for n := 0; n < count; n++ {
					extra[fmt.Sprint(n)] = fmt.Sprint(id, n)
				}
				r.States[id] = repository.QuestState{Status: "completed", Extra: extra, UpdatedAt: time.Unix(id, 0).UTC()}
			}
			v := r.States[1]
			v.Status = "active"
			r.States[1] = v
			t.Setenv("DNFBRIDGE_MYSQL_INSERT_BATCH", "200")
			if err := g.Quest.Save(ctx, r); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DNFBRIDGE_MYSQL_INSERT_BATCH", "")
			load := func() repository.QuestRecord {
				t.Helper()
				v, found, e := open().Quest.Load(ctx, "10")
				if e != nil || !found {
					t.Fatal(found, e)
				}
				return v
			}
			before := load()
			characterBefore, _, err := g.Character.Load(ctx, "10")
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := quest.CatalogFromDefinitions([]quest.Definition{{ID: 1, Path: "n_quest/one.qst", Type: "[meet npc]", LevelMin: 1, LevelMax: 100}})
			if err != nil {
				t.Fatal(err)
			}
			u := g.Character.(repository.CharacterManagementUnitOfWork)
			cmd := adminops.QuestCommand{AccountID: c.AccountID, CharacterID: "10", Action: "complete", QuestIDs: []int64{1}, PVFChecksum: strings.Repeat("a", 64), Reason: "isolated regression", Actor: "fixture"}
			preview, err := adminops.RunQuestOperation(ctx, u, catalog, cmd, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Key = "quest-large-history-request"
			cmd.Revision = preview.Revision
			calls := 0
			_, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, func(context.Context, string) error {
				calls++
				if calls == 2 {
					return context.DeadlineExceeded
				}
				return nil
			})
			if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(before, load()) {
				t.Fatal("timed-out write did not roll back", err)
			}
			rows, err := u.CharacterManagementLog(ctx, "10", 0, 10)
			if err != nil || len(rows) != 0 {
				t.Fatal("timeout left receipt", rows, err)
			}
			start := time.Now()
			receipt, err := adminops.RunQuestOperation(ctx, u, catalog, cmd, true, func(context.Context, string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("complete with 769 historical states: %v", time.Since(start))
			after := load()
			if after.States[1].Status != "completed" || receipt.RewardGranted {
				t.Fatal("completion", receipt, after.States[1])
			}
			for id, v := range before.States {
				if id != 1 && !reflect.DeepEqual(v, after.States[id]) {
					t.Fatalf("history changed: %d", id)
				}
			}
			if !reflect.DeepEqual(before.Progress, after.Progress) {
				t.Fatal("unrelated progress changed")
			}
			characterAfter, _, err := g.Character.Load(ctx, "10")
			if err != nil || !reflect.DeepEqual(characterBefore, characterAfter) {
				t.Fatal("character assets changed", err)
			}
			// A replay after losing the response returns the durable receipt even when offline/PVF checks are unavailable.
			replayed, found, err := adminops.LookupQuestOperation(ctx, u, cmd)
			if err != nil || !found || !reflect.DeepEqual(receipt, replayed) {
				t.Fatal("receipt recovery", found, err)
			}
			replayed, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, nil)
			if err != nil || !reflect.DeepEqual(receipt, replayed) {
				t.Fatal("idempotent replay", err)
			}
			rows, err = u.CharacterManagementLog(ctx, "10", 0, 10)
			if err != nil || len(rows) != 1 {
				t.Fatal("duplicate receipt", err, len(rows))
			}
			cmd.Action = "reset"
			cmd.Key = "quest-large-history-reset"
			cmd.Revision = receipt.Revision
			if _, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, func(context.Context, string) error { return nil }); err != nil {
				t.Fatal(err)
			}
			reset := load()
			if _, exists := reset.States[1]; exists {
				t.Fatal("reset retained task")
			}
			delete(after.States, 1)
			if !reflect.DeepEqual(after.States, reset.States) || !reflect.DeepEqual(after.Progress, reset.Progress) {
				t.Fatal("reset changed unrelated rows")
			}
		})
	}
}
