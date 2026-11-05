package gmtool

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Hundreds of rows and their extras must stay untouched when editing one slot.
// SQL triggers reject a rewrite even when it would restore identical values.
func TestV1CharacterInventorySelectedSlotsMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open, databases := currencyFixtureDatabases(t, shards)
			ctx := context.Background()
			g := open()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "fixture", Job: "0", Level: 90}))
			source := newItemCacheTestSource(readonlyTestSource{})
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
			s.pvf = newPVFCache()
			s.items = newItemIndex("", s, func(string, ...any) {})
			s.items.setEntriesLocked(itemcfgTestEntries())
			raw := make([]byte, 119)
			binary.LittleEndian.PutUint32(raw[2:6], itemcfgTestWeaponID)
			raw[90] = 0xad
			bag := repository.InventoryRecord{CharacterID: "10", Slots: map[string]repository.ItemStack{}, Warehouse: map[string]repository.ItemStack{}}
			for i := 0; i < 400; i++ {
				extra := map[string]string{}
				for j := 0; j < 12; j++ {
					extra[fmt.Sprint(j)] = strings.Repeat("x", 40)
				}
				bag.Slots[fmt.Sprintf("12:%d", i)] = repository.ItemStack{ItemID: 700, Count: 1, Extra: extra}
			}
			target := repository.ItemStack{ItemID: itemcfgTestWeaponID, Count: 1, RawEntry: raw, Extra: map[string]string{"serial": "fixture-unique"}}
			bag.Slots["0:9"] = target
			bag.Warehouse["2:0"] = target
			bag.Warehouse["2:1"] = target
			must(g.Inventory.Save(ctx, bag))
			load := func() repository.InventoryRecord { v, _, err := open().Inventory.Load(ctx, "10"); must(err); return v }
			before := load()
			for _, db := range databases {
				for _, table := range []string{"dnf_inventory_items", "dnf_inventory_item_extra"} {
					for _, event := range []string{"DELETE", "INSERT", "UPDATE"} {
						row := "NEW"
						if event == "DELETE" {
							row = "OLD"
						}
						_, err := s.db.Exec(fmt.Sprintf("CREATE TRIGGER `%s`.protect_%s_%s BEFORE %s ON `%s`.%s FOR EACH ROW BEGIN IF NOT ((%s.collection_name='slots' AND %s.entry_key='0:9') OR (%s.collection_name='warehouse' AND %s.entry_key='2:0')) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='unrelated character slot touched'; END IF; END", db, table, event, event, db, table, row, row, row, row))
						must(err)
					}
				}
			}
			u := g.Character.(repository.InventoryManagementUnitOfWork)
			level := 31
			cmd := adminops.InventoryCommand{AccountID: "wallet:1", CharacterID: "10", Action: "configure", Items: []adminops.InventorySelection{{Container: "equipment", Slot: 9, Revision: adminops.InventoryRevision("wallet:1", "10", "equipment", 9, before.Slots["0:9"])}, {Container: "warehouse", Slot: 0, Revision: adminops.InventoryRevision("wallet:1", "10", "warehouse", 0, before.Warehouse["2:0"])}}, Options: &adminops.InventoryOptions{UpgradeLevel: &level}, PVFChecksum: source.checksum, Reason: "fixture", Actor: "fixture", PlanToken: "fixture-plan", Key: "character-delta-fixture-01"}
			mutate := s.v1InventoryMutator(time.Now().UTC(), "fixture")
			run := func(unit repository.InventoryManagementUnitOfWork) (adminops.InventoryReceipt, error) {
				return adminops.RunInventoryOperation(ctx, unit, cmd, true, mutate, s.v1InventoryProject, func(context.Context, string) error { return nil })
			}
			_, err := run(inventoryFailingUOW{u})
			if err == nil {
				t.Fatal("expected receipt failure")
			}
			if !reflect.DeepEqual(before, load()) {
				t.Fatal("receipt failure did not roll back")
			}
			receipt, err := run(u)
			must(err)
			after := load()
			expected := repository.CloneInventory(before)
			expected.Slots["0:9"], err = mutate(ctx, cmd.Items[0], expected.Slots["0:9"], cmd.Options)
			must(err)
			expected.Warehouse["2:0"], err = mutate(ctx, cmd.Items[1], expected.Warehouse["2:0"], cmd.Options)
			must(err)
			expected.UpdatedAt = after.UpdatedAt
			if !reflect.DeepEqual(expected, after) {
				t.Fatal("reinforce changed unrelated data")
			}
			if got := receipt.Changes[0].After.Attributes["reinforce"]; got != 31 {
				t.Fatalf("reinforce=%v", got)
			}
			replay, err := run(u)
			must(err)
			left, _ := json.Marshal(receipt)
			right, _ := json.Marshal(replay)
			if string(left) != string(right) {
				t.Fatal("replay changed result")
			}
			cmd.Action = "delete"
			cmd.Options = nil
			cmd.Key = "character-delta-fixture-02"
			cmd.Items[0].Revision = adminops.InventoryRevision("wallet:1", "10", "equipment", 9, after.Slots["0:9"])
			cmd.Items[1].Revision = adminops.InventoryRevision("wallet:1", "10", "warehouse", 0, after.Warehouse["2:0"])
			_, err = run(u)
			must(err)
			deleted := load()
			delete(expected.Slots, "0:9")
			delete(expected.Warehouse, "2:0")
			expected.UpdatedAt = deleted.UpdatedAt
			if !reflect.DeepEqual(expected, deleted) {
				t.Fatal("delete changed unrelated data")
			}
		})
	}
}
