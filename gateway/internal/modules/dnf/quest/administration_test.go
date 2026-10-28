package quest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestAdministrationLinkedResetAndSettlement(t *testing.T) {
	ctx := context.Background()
	catalog := linkedChainTestCatalog(t)
	character := repository.CharacterRecord{CharacterID: "1", AccountID: "admin-test", Job: "11", Level: 90}
	before := linkedChainTestRecord()
	snapshot := repository.CloneQuest(before)
	next, err := PlanAdministration(ctx, catalog, character, before, "reset", []int64{101})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{100, 101, 102} {
		if next.States[id].Status != "active" {
			t.Fatalf("family %d not active: %+v", id, next.States[id])
		}
	}
	for _, id := range []int64{103, 104, 105, 107, 200, 201} {
		if !reflect.DeepEqual(before.States[id], next.States[id]) {
			t.Fatalf("unrelated quest %d changed", id)
		}
	}
	if !reflect.DeepEqual(before, snapshot) {
		t.Fatal("planner mutated its input")
	}
	if _, err = PlanAdministration(ctx, catalog, character, before, "reset", []int64{100, 101}); err != nil {
		t.Fatal("selecting parent and child must reset their family once", err)
	}
	if _, err = PlanAdministration(ctx, catalog, character, before, "reset", []int64{101, 103}); !errors.Is(err, ErrQuestChainRequestedNotManaged) {
		t.Fatal("unmanaged sibling was skipped", err)
	}
	before.States[141] = repository.QuestState{Status: "completed"}
	if _, err = PlanAdministration(ctx, catalog, character, before, "reset", []int64{141}); !errors.Is(err, ErrQuestChainChildUnresolved) {
		t.Fatal(err)
	}
	var operation *AdministrationError
	if !errors.As(err, &operation) || operation.QuestID != 141 {
		t.Fatal("missing failing quest", err)
	}
	before.States[105] = repository.QuestState{Status: "active", ProgressValue: 9, Extra: map[string]string{"completion_key": "original-proof", "reward_state": "pending", "settlement_receipt_v1": "existing"}}
	if _, err = PlanAdministration(ctx, catalog, character, before, "ready", []int64{105}); !errors.Is(err, ErrAdministrationSettlement) {
		t.Fatal(err)
	}
	next, err = PlanAdministration(ctx, catalog, character, before, "complete", []int64{105})
	if err != nil {
		t.Fatal(err)
	}
	if next.States[105].Extra["completion_key"] != "original-proof" || next.States[105].Extra["settlement_receipt_v1"] != "existing" {
		t.Fatal("settlement proof removed")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = PlanAdministration(cancelled, catalog, character, before, "complete", []int64{105}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAdministrationCompletionCannotGrantOrdinaryReward(t *testing.T) {
	ctx := context.Background()
	repos := finishOwnerSeed(t, ctx)
	character, _, err := repos.Character.Load(ctx, "19")
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := repos.Quest.Load(ctx, "19")
	if err != nil {
		t.Fatal(err)
	}
	catalog := finishOwnerCatalog()
	next, err := PlanAdministration(ctx, catalog, character, before, "complete", []int64{3145})
	if err != nil {
		t.Fatal(err)
	}
	if err = repos.Quest.Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	owner, err := NewOwner(repos)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ApplyFinishSettlement(ctx, catalog, finishOwnerInput(finishOwnerProgression(t), finishOwnerAllocator(nil))); err == nil {
		t.Fatal("administrative completion granted ordinary rewards")
	}
	after, _, err := repos.Character.Load(ctx, "19")
	if err != nil || !reflect.DeepEqual(character, after) {
		t.Fatal("character assets changed", err)
	}
}

func TestAdministrationSlotExpansionUsesPVF(t *testing.T) {
	character := repository.CharacterRecord{CharacterID: "10", AccountID: "fixture", Job: "0", Level: 90, Stats: map[string]int64{"ex_equip_slot_stat": 128}}
	for _, tc := range []struct {
		name string
		data []int64
		want byte
		bad  bool
	}{
		{"left", []int64{0}, ExEquipSlotSupport, false},
		{"right", []int64{1}, ExEquipSlotMagicStone, false},
		{"earring", []int64{2}, ExEquipSlotEarring, false},
		{"missing", nil, 0, true},
		{"negative", []int64{-1}, 0, true},
		{"unknown", []int64{3}, 0, true},
		{"multiple", []int64{0, 1}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := CatalogFromDefinitions([]Definition{{ID: 371, Path: "n_quest/slot.qst", RewardType: "[Slot Expansion]", RewardIntData: tc.data}, {ID: 372, Path: "n_quest/item.qst", RewardType: "[item]"}})
			if err != nil {
				t.Fatal(err)
			}
			bits, err := AdministrationSlotExpansion(catalog, character, "complete", []int64{372, 371})
			if bits != tc.want || errors.Is(err, ErrQuestRewardMalformed) != tc.bad || (!tc.bad && err != nil) {
				t.Fatal(bits, err)
			}
			if bits, err = AdministrationSlotExpansion(catalog, character, "reset", []int64{371}); err != nil || bits != 0 {
				t.Fatal("reset generated an unlock", bits, err)
			}
			if character.Stats["ex_equip_slot_stat"] != 128 {
				t.Fatal("planner mutated character")
			}
		})
	}
}
