package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1QuestSlotExpansionMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			ctx := context.Background()
			g := open()
			character := repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "开槽测试", Level: 90, Job: "0", Stats: map[string]int64{"ex_equip_slot_stat": 128, "gold": 12345, "exp": 7654, "unrelated_stat": 99}}
			if err := g.Character.Save(ctx, character); err != nil {
				t.Fatal(err)
			}
			// Non-production IDs prove that the reward definition, not a fixed list,
			// determines the slots. The left quest was completed by the old GM.
			q := repository.QuestRecord{CharacterID: "10", States: map[int64]repository.QuestState{
				71: {Status: "completed", Extra: map[string]string{"gm_force_completed": "true", "gm_rewards_granted": "false", "reward_state": "granted", "proof": "keep"}},
				72: {Status: "active", ProgressValue: 3},
				79: {Status: "completed", Extra: map[string]string{"settlement_receipt_v1": "keep-unrelated-receipt"}},
			}}
			if err := g.Quest.Save(ctx, q); err != nil {
				t.Fatal(err)
			}
			catalog, err := quest.CatalogFromDefinitions([]quest.Definition{
				{ID: 71, Path: "n_quest/left.qst", RewardType: "[slot expansion]", RewardIntData: []int64{0}},
				{ID: 72, Path: "n_quest/right.qst", RewardType: "[slot expansion]", RewardIntData: []int64{1}},
				{ID: 73, Path: "n_quest/ear.qst", RewardType: "[slot expansion]", RewardIntData: []int64{2}},
				{ID: 74, Path: "n_quest/bad.qst", RewardType: "[slot expansion]", RewardIntData: []int64{3}},
			})
			if err != nil {
				t.Fatal(err)
			}
			u := g.Character.(repository.CharacterManagementUnitOfWork)
			load := func() (repository.CharacterRecord, repository.QuestRecord) {
				c, found, err := open().Character.Load(ctx, "10")
				if err != nil || !found {
					t.Fatal(err)
				}
				q, found, err := open().Quest.Load(ctx, "10")
				if err != nil || !found {
					t.Fatal(err)
				}
				return c, q
			}
			beforeC, beforeQ := load()
			unchanged := func() {
				t.Helper()
				c, q := load()
				if !reflect.DeepEqual(c, beforeC) || !reflect.DeepEqual(q, beforeQ) {
					t.Fatal("operation changed state before commit/after rollback")
				}
				rows, err := u.CharacterManagementLog(ctx, "10", 0, 10)
				if err != nil || len(rows) != 0 {
					t.Fatal("unexpected receipt", rows, err)
				}
			}
			cmd := adminops.QuestCommand{AccountID: "wallet:1", CharacterID: "10", Action: "complete", QuestIDs: []int64{71, 72}, PVFChecksum: strings.Repeat("a", 64), Reason: "补齐开槽", Actor: "测试管理员", Key: "slot-complete-request-01"}
			preview, err := adminops.RunQuestOperation(ctx, u, catalog, cmd, false, nil)
			if err != nil || preview.EquipmentSlots == nil || *preview.EquipmentSlots != (adminops.QuestEquipmentSlots{Before: 128, After: 131, Requested: 3}) {
				t.Fatal(preview, err)
			}
			unchanged()
			cmd.Revision = preview.Revision
			offline := func(context.Context, string) error { return nil }
			if _, err = adminops.RunQuestOperation(ctx, questFailingUOW{u}, catalog, cmd, true, offline); err == nil {
				t.Fatal("receipt failure accepted")
			}
			unchanged()
			calls := 0
			if _, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, func(context.Context, string) error {
				calls++
				if calls == 2 {
					return adminops.ErrCharacterOnline
				}
				return nil
			}); !errors.Is(err, adminops.ErrCharacterOnline) {
				t.Fatal(err)
			}
			unchanged()
			bad := cmd
			bad.QuestIDs = []int64{71, 74}
			if _, err = adminops.RunQuestOperation(ctx, u, catalog, bad, true, offline); !errors.Is(err, quest.ErrQuestRewardMalformed) {
				t.Fatal(err)
			}
			unchanged()
			result, err := adminops.RunQuestOperation(ctx, u, catalog, cmd, true, offline)
			if err != nil || result.EquipmentSlots == nil || result.EquipmentSlots.After != 131 || result.RewardGranted {
				t.Fatal(result, err)
			}
			afterC, afterQ := load()
			if afterC.Stats["ex_equip_slot_stat"] != 131 || afterQ.States[72].Status != "completed" || !reflect.DeepEqual(beforeQ.States[71], afterQ.States[71]) || !reflect.DeepEqual(beforeQ.States[79], afterQ.States[79]) {
				t.Fatal("completion/repair lost history", afterC, afterQ)
			}
			comparison := repository.CloneCharacter(afterC)
			comparison.Stats["ex_equip_slot_stat"] = beforeC.Stats["ex_equip_slot_stat"]
			comparison.UpdatedAt = beforeC.UpdatedAt
			if !reflect.DeepEqual(beforeC, comparison) {
				a, b := reflect.ValueOf(beforeC), reflect.ValueOf(comparison)
				for i := 0; i < a.NumField(); i++ {
					if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
						t.Errorf("unlock changed %s: before=%v after=%v", a.Type().Field(i).Name, a.Field(i).Interface(), b.Field(i).Interface())
					}
				}
				t.FailNow()
			}
			if result.Revision != adminops.QuestRevision(afterC, afterQ, cmd.PVFChecksum) {
				t.Fatal("receipt revision does not match persisted state")
			}
			replay, found, err := adminops.LookupQuestOperation(ctx, open().Character.(repository.CharacterManagementUnitOfWork), cmd)
			if err != nil || !found || !reflect.DeepEqual(replay, result) {
				t.Fatal("durable replay changed", replay, err)
			}
			cmd.Key = "slot-repeat-request-02"
			cmd.Revision = result.Revision
			result, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, offline)
			if err != nil || result.EquipmentSlots.Before != 131 || result.EquipmentSlots.After != 131 {
				t.Fatal(result, err)
			}
			if c, _ := load(); !reflect.DeepEqual(c, afterC) {
				t.Fatal("already-open slot rewrote character")
			}
			cmd.Action, cmd.Key = "reset", "slot-reset-request-03"
			cmd.Revision = result.Revision
			result, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, offline)
			if err != nil || result.EquipmentSlots != nil {
				t.Fatal(result, err)
			}
			if c, _ := load(); c.Stats["ex_equip_slot_stat"] != 131 {
				t.Fatal("reset closed equipment slots")
			}
			// Real HTTP + indexed PVF: preview, commit, recovery, malformed reward.
			source := newItemCacheTestSource(readonlyTestSource{
				"n_quest/quest.lst": "73 `ear.qst`\n74 `bad.qst`\n",
				"n_quest/ear.qst":   "#PVF_File\n[name]\n`耳环`\n[reward type]\n`[slot expansion]`\n[reward int data]\n2\n",
				"n_quest/bad.qst":   "#PVF_File\n[name]\n`错误奖励`\n[reward type]\n`[slot expansion]`\n[reward int data]\n3\n",
			})
			path := filepath.Join(t.TempDir(), "items.json")
			if _, err = BuildQuestIndex(ctx, source, path); err != nil {
				t.Fatal(err)
			}
			s.items = newItemIndex(path, s, nil)
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeAccountEditFixture(t, w, r, false) }))
			defer runtime.Close()
			s.deps.Runtime, err = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			token, _ := s.startSession()
			body := map[string]any{"accountId": "wallet:1", "action": "complete", "questIds": []string{"73"}, "reason": "耳环修复"}
			raw, _ := json.Marshal(body)
			status, out := operationCall(s, token, "characters/10/quest-operations/preview", string(raw), "", nil)
			if status != 200 {
				t.Fatal(status, out)
			}
			data := out["data"].(map[string]any)
			if data["equipmentSlots"].(map[string]any)["after"] != float64(147) {
				t.Fatal(data)
			}
			if c, _ := load(); c.Stats["ex_equip_slot_stat"] != 131 {
				t.Fatal("HTTP preview wrote slots")
			}
			body["revision"], body["pvfChecksum"] = data["revision"], data["pvfChecksum"]
			raw, _ = json.Marshal(body)
			status, out = operationCall(s, token, "characters/10/quest-operations", string(raw), "slot-http-request-04", nil)
			if status != 200 {
				t.Fatal(status, out)
			}
			if c, q := load(); c.Stats["ex_equip_slot_stat"] != 147 || q.States[73].Status != "completed" {
				t.Fatal("HTTP did not unlock earring")
			}
			s.deps.PVF, s.deps.Runtime = nil, nil
			status, out = operationCall(s, token, "characters/10/quest-operations", string(raw), "slot-http-request-04", nil)
			if status != 200 || out["data"].(map[string]any)["equipmentSlots"].(map[string]any)["after"] != float64(147) {
				t.Fatal("HTTP recovery", status, out)
			}
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
			body["questIds"] = []string{"74"}
			raw, _ = json.Marshal(body)
			status, out = operationCall(s, token, "characters/10/quest-operations/preview", string(raw), "", nil)
			if status != 422 {
				t.Fatal("malformed PVF", status, out)
			}
			// A changed slot bit alone invalidates the old preview. Mutate just
			// that column, so aggregate-save normalization cannot affect the test.
			cmd.Action, cmd.Key = "complete", "slot-stale-request-05"
			preview, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Revision = preview.Revision
			if err = u.WithinCharacterManagement(ctx, "wallet:1", "10", func(g repository.Group, _ repository.ManagementReceiptStore) error {
				return g.Character.(repository.CharacterEquipmentSlotWriter).UnlockEquipmentSlots(ctx, "10", 64)
			}); err != nil {
				t.Fatal(err)
			}
			beforeC, beforeQ = load()
			if _, err = adminops.RunQuestOperation(ctx, u, catalog, cmd, true, offline); !errors.Is(err, adminops.ErrConflict) {
				t.Fatal("stale slot preview accepted", err)
			}
			if c, q := load(); !reflect.DeepEqual(c, beforeC) || !reflect.DeepEqual(q, beforeQ) {
				t.Fatal("stale operation changed data")
			}
		})
	}
}
