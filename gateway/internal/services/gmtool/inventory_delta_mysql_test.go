package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestV1AccountInventorySelectedSlotsMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open, databases := currencyFixtureDatabases(t, shards)
			ctx := context.Background()
			g := open()
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			must(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "fixture", Job: "0"}))
			a := repository.AccountInventoryRecord{AccountID: "wallet:1", Slots: map[string]repository.ItemStack{}}
			for i := 0; i < 18; i++ {
				extras := map[string]string{}
				for j := 0; j < 10; j++ {
					extras[fmt.Sprint(j)] = strings.Repeat("x", 40)
				}
				a.Slots[fmt.Sprintf("12:%d", i)] = repository.ItemStack{ItemID: 700, Count: 10, Extra: extras}
			}
			a.Slots["0:358"] = repository.ItemStack{ItemID: 3037, Count: 33541742, Extra: map[string]string{"quantity": "33541742"}}
			must(g.AccountInventory.Save(ctx, a))
			before, _, e := g.AccountInventory.Load(ctx, a.AccountID)
			must(e)
			// Every unrelated row rejects DELETE/INSERT/UPDATE. A full aggregate rewrite
			// cannot pass this regression, even if it would reconstruct identical values.
			for _, db := range databases {
				for _, table := range []string{"dnf_account_inventory_items", "dnf_account_inventory_item_extra"} {
					for _, event := range []string{"DELETE", "INSERT", "UPDATE"} {
						row := "NEW"
						if event == "DELETE" {
							row = "OLD"
						}
						mustExec := fmt.Sprintf("CREATE TRIGGER `%s`.protect_%s_%s BEFORE %s ON `%s`.%s FOR EACH ROW BEGIN IF %s.entry_key <> '0:358' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='unrelated slot touched'; END IF; END", db, table, event, event, db, table, row)
						_, e = s.db.Exec(mustExec)
						must(e)
					}
				}
			}
			u := g.AccountInventory.(repository.InventoryManagementUnitOfWork)
			count := "41742"
			cmd := adminops.InventoryCommand{AccountID: a.AccountID, CharacterID: "10", Action: "configure", Items: []adminops.InventorySelection{{Container: "shared", Slot: 358, Revision: adminops.InventorySlotRevision(a.AccountID, "10", "shared", 358, before.Slots["0:358"], true)}}, Options: &adminops.InventoryOptions{Count: &count}, PVFChecksum: strings.Repeat("a", 64), Reason: "fixture", Actor: "fixture", PlanToken: "fixture-plan", Key: "shared-delta-fixture-01", Force: true}
			mutate := func(_ context.Context, _ adminops.InventorySelection, v repository.ItemStack, _ *adminops.InventoryOptions) (repository.ItemStack, error) {
				v.Count = 41742
				v.Extra["quantity"] = "41742"
				return v, nil
			}
			offline := func(context.Context, string) error { return nil }
			run := func(unit repository.InventoryManagementUnitOfWork) (adminops.InventoryReceipt, error) {
				return adminops.RunInventoryOperation(ctx, unit, cmd, true, mutate, s.v1InventoryProject, offline)
			}
			_, e = run(inventoryFailingUOW{u})
			if e == nil {
				t.Fatal("expected receipt failure")
			}
			rolled, _, e := g.AccountInventory.Load(ctx, a.AccountID)
			must(e)
			if !reflect.DeepEqual(rolled, before) {
				t.Fatal("receipt failure did not roll back")
			}
			r, e := run(u)
			must(e)
			after, _, e := g.AccountInventory.Load(ctx, a.AccountID)
			must(e)
			want := repository.CloneAccountInventory(before)
			v := want.Slots["0:358"]
			v.Count = 41742
			v.Extra["quantity"] = "41742"
			want.Slots["0:358"] = v
			want.UpdatedAt = after.UpdatedAt
			if !reflect.DeepEqual(after, want) {
				t.Fatal("unexpected inventory delta")
			}
			again, e := run(u)
			must(e)
			if !reflect.DeepEqual(again, r) {
				t.Fatal("replay changed receipt")
			}
			cmd.Key = "shared-delta-fixture-02"
			cmd.Items[0].Revision = adminops.InventorySlotRevision(a.AccountID, "10", "shared", 358, after.Slots["0:358"], true)
			cmd.Action = "delete"
			cmd.Options = nil
			_, e = run(u)
			must(e)
			deleted, _, e := g.AccountInventory.Load(ctx, a.AccountID)
			must(e)
			delete(want.Slots, "0:358")
			want.UpdatedAt = deleted.UpdatedAt
			if !reflect.DeepEqual(deleted, want) {
				t.Fatal("delete changed another slot")
			}
		})
	}
}
