package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1InventoryForceMySQL(t *testing.T) {
	for _, loseLease := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit-and-replay", true: "lease-lost-rollback"}[loseLease], func(t *testing.T) {
			s, _, open := currencyFixture(t, 1)
			ctx := context.Background()
			g := open()
			if err := g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "fixture", Job: "0"}); err != nil {
				t.Fatal(err)
			}
			stack := repository.ItemStack{ItemID: itemcfgTestPotionID, Count: 5}
			if err := g.Inventory.Save(ctx, repository.InventoryRecord{CharacterID: "10", Slots: map[string]repository.ItemStack{"0:65": stack}}); err != nil {
				t.Fatal(err)
			}
			inv, _, _ := g.Inventory.Load(ctx, "10")
			stack = inv.Slots["0:65"]
			source := newItemCacheTestSource(readonlyTestSource{})
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
			s.pvf = newPVFCache()
			s.items = newItemIndex("", s, func(string, ...any) {})
			s.items.setEntriesLocked(itemcfgTestEntries())
			var began, released, checks atomic.Int32
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req adminapi.Request
				json.NewDecoder(r.Body).Decode(&req)
				out := adminapi.Response{Version: 1, OK: true}
				switch req.Action {
				case "character_edit_status":
					out.CharacterEdit = &adminapi.CharacterEditStatus{Protocol: adminapi.CharacterEditProtocol, AccountID: req.AccountID, CharacterID: req.CharacterID, Busy: true}
				case "inventory_edit_capability":
					out.InventoryEdit = &adminapi.InventoryEditStatus{Protocol: adminapi.InventoryEditProtocol}
				case "inventory_edit_begin", "inventory_edit_status", "inventory_edit_release":
					if req.Action == "inventory_edit_begin" {
						began.Add(1)
					}
					if req.Action == "inventory_edit_release" {
						released.Add(1)
					}
					ready := req.Action != "inventory_edit_begin"
					if req.Action == "inventory_edit_status" && checks.Add(1) >= 4 && loseLease {
						out.OK = false
					}
					out.InventoryEdit = &adminapi.InventoryEditStatus{Protocol: adminapi.InventoryEditProtocol, AccountID: req.AccountID, Token: req.InventoryEdit.Token, Ready: ready, ExpiresAt: time.Now().Add(time.Minute)}
				default:
					t.Errorf("unexpected runtime action %s", req.Action)
				}
				json.NewEncoder(w).Encode(out)
			}))
			defer runtime.Close()
			s.deps.Runtime, _ = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			token, _ := s.startSession()
			body := map[string]any{"accountId": "wallet:1", "action": "delete", "items": []adminops.InventorySelection{{Container: "consumables", Slot: 65, Revision: adminops.InventoryRevision("wallet:1", "10", "consumables", 65, stack)}}, "pvfChecksum": source.checksum, "reason": "fixture"}
			encode := func() string { raw, _ := json.Marshal(body); return string(raw) }
			status, out := operationCall(s, token, "characters/10/inventory-operations/preview", encode(), "", nil)
			if status != 200 || out["data"].(map[string]any)["forceAvailable"] != true {
				t.Fatal(status, out)
			}
			body["planToken"] = out["data"].(map[string]any)["planToken"]
			if status, _ := operationCall(s, token, "characters/10/inventory-operations", encode(), "normal-online-request", nil); status != 409 {
				t.Fatal("normal edit bypassed online guard", status)
			}
			body["force"] = true
			if status, _ := operationCall(s, token, "characters/10/inventory-operations", encode(), "tampered-force-request", nil); status != 409 || began.Load() != 0 {
				t.Fatal("force not bound to plan", status)
			}
			delete(body, "planToken")
			status, out = operationCall(s, token, "characters/10/inventory-operations/preview", encode(), "", nil)
			if status != 200 {
				t.Fatal(status, out)
			}
			body["planToken"] = out["data"].(map[string]any)["planToken"]
			status, out = operationCall(s, token, "characters/10/inventory-operations", encode(), "forced-edit-request", nil)
			inv, _, err := g.Inventory.Load(ctx, "10")
			if err != nil {
				t.Fatal(err)
			}
			if released.Load() != 1 || began.Load() != 1 {
				t.Fatal("lease lifecycle", began.Load(), released.Load())
			}
			if loseLease {
				if status == 200 || len(inv.Slots) != 1 {
					t.Fatal("lost lease changed inventory", status, inv)
				}
				return
			}
			if status != 200 || len(inv.Slots) != 0 || out["data"].(map[string]any)["forced"] != true {
				t.Fatal("force failed", status, out)
			}
			s.deps.Runtime = nil
			s.deps.PVF = nil
			status, out = operationCall(s, token, "characters/10/inventory-operations", encode(), "forced-edit-request", nil)
			if status != 200 || began.Load() != 1 {
				t.Fatal("replay needs runtime or disconnects again", status, out)
			}
		})
	}
}
