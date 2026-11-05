package gmtool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1SystemModeOnlineRenameMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			ctx := context.Background()
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			check(open().Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "原名", Job: "0", Level: 90}))
			check(repository.CreateCharacter(ctx, open().Character, repository.CharacterRecord{CharacterID: "11", AccountID: "wallet:1", Slot: 1, Name: "已占用", Job: "0", Level: 90}))
			calls := new(atomic.Int64)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeAccountEditFixture(t, w, r, true) }))
			defer runtime.Close()
			s.deps.Runtime, _ = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			token, err := s.startSession()
			check(err)
			policy, err := s.loadSystemSettings(ctx)
			check(err)
			if policy.CarbonMode {
				t.Fatal("default enabled")
			}
			body := map[string]string{"accountId": "wallet:1", "expectedName": "原名", "newName": "第一名字", "reason": "fixture"}
			rename := func(key string) (int, map[string]any) {
				raw, _ := json.Marshal(body)
				return operationCall(s, token, "characters/10/rename-operations", string(raw), key, nil)
			}
			if code, out := rename("carbon-disabled-rename"); code != 409 || out["error"].(map[string]any)["code"] != "character_online" {
				t.Fatal(code, out)
			}
			if code, _ := operationCall(s, token, "settings/system", `{"carbonMode":true}`, "", map[string]string{"X-CSRF-Token": ""}); code != 403 {
				t.Fatal("csrf", code)
			}
			if code, out := operationCall(s, token, "settings/system", `{"carbonMode":true}`, "", nil); code != 200 {
				t.Fatal(code, out)
			}
			before := calls.Load()
			for i, name := range []string{"第一名字", "第二名字"} {
				body["newName"] = name
				if code, out := rename(fmt.Sprintf("carbon-rename-%016d", i)); code != 200 {
					actual, _, _ := open().Character.Load(ctx, "10")
					t.Fatalf("rename %d expected=%q actual=%q: %d %v", i, body["expectedName"], actual.Name, code, out)
				}
				body["expectedName"] = name
			}
			body["newName"] = "已占用"
			if code, out := rename("carbon-duplicate-rename"); code != 409 || out["error"].(map[string]any)["code"] != "name_taken" {
				t.Fatal(code, out)
			}
			if calls.Load() != before {
				t.Fatal("carbon mode contacted game runtime")
			}
			// A new Server instance sees the persisted mode without a config change.
			restarted := &Server{deps: s.deps}
			policy, err = restarted.loadSystemSettings(ctx)
			check(err)
			if !policy.CarbonMode {
				t.Fatal("policy not persisted")
			}
			for _, checkEdit := range []adminops.QuestOfflineCheck{s.v1CharacterEditCheck("wallet:1"), s.v1AccountEditCheck("wallet:1")} {
				check(checkEdit(context.WithValue(ctx, carbonModeKey{}, true), "10"))
			}
			if calls.Load() != before {
				t.Fatal("global checks contacted game")
			}
			if code, out := operationCall(s, token, "settings/system", `{"carbonMode":false}`, "", nil); code != 200 {
				t.Fatal(code, out)
			}
			body["newName"] = "第三名字"
			if code, _ := rename("carbon-disabled-again"); code != 409 {
				t.Fatal(code)
			}
			rows, err := open().Character.(repository.CharacterRenameUnitOfWork).CharacterRenameLog(ctx, "10", 0, 20)
			check(err)
			if len(rows) != 2 {
				t.Fatal("receipts", len(rows))
			}
		})
	}
}

func TestV1CarbonInventoryAndCurrencyProjectionMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			ctx := context.Background()
			g := open()
			check := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			check(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "fixture", Job: "0"}))
			check(g.Inventory.Save(ctx, repository.InventoryRecord{CharacterID: "10", Slots: map[string]repository.ItemStack{"0:65": {ItemID: itemcfgTestPotionID, Count: 5}}}))
			inv, _, err := g.Inventory.Load(ctx, "10")
			check(err)
			source := newItemCacheTestSource(readonlyTestSource{})
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
			s.pvf = newPVFCache()
			s.items = newItemIndex("", s, func(string, ...any) {})
			s.items.setEntriesLocked(itemcfgTestEntries())
			var calls atomic.Int64
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeAccountEditFixture(t, w, r, true) }))
			defer runtime.Close()
			s.deps.Runtime, _ = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			token, err := s.startSession()
			check(err)
			if code, out := operationCall(s, token, "settings/system", `{"carbonMode":true}`, "", nil); code != 200 {
				t.Fatal(code, out)
			}
			body := map[string]any{"accountId": "wallet:1", "action": "delete", "items": []adminops.InventorySelection{{Container: "consumables", Slot: 65, Revision: adminops.InventoryRevision("wallet:1", "10", "consumables", 65, inv.Slots["0:65"])}}, "pvfChecksum": source.checksum, "reason": "fixture"}
			encode := func() string { b, _ := json.Marshal(body); return string(b) }
			status, out := operationCall(s, token, "characters/10/inventory-operations/preview", encode(), "", nil)
			if status != 200 {
				t.Fatal(status, out)
			}
			body["planToken"] = out["data"].(map[string]any)["planToken"]
			for range 2 {
				if code, out := operationCall(s, token, "characters/10/inventory-operations", encode(), "carbon-inventory-request", nil); code != 200 {
					t.Fatal(code, out)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("unexpected runtime request", calls.Load())
			}
			inv, _, err = open().Inventory.Load(ctx, "10")
			check(err)
			if _, exists := inv.Slots["0:65"]; exists {
				t.Fatal("item not removed")
			}
			a, _, err := g.Account.Load(ctx, "wallet:1")
			check(err)
			if a.Metadata == nil {
				a.Metadata = map[string]string{}
			}
			a.Metadata["account_cera"] = "123"
			a.Metadata["gm_currency_sequence_v1"] = "7"
			for i := range 300 {
				a.Metadata[fmt.Sprintf("unrelated_%d", i)] = strings.Repeat("x", 100)
			}
			check(g.Account.Save(ctx, a))
			v, found, err := open().Account.(repository.AccountCurrencyReader).LoadAccountCurrency(ctx, a.AccountID)
			check(err)
			if !found || len(v.Metadata) != 2 || adminops.CurrencyRevision(v) != adminops.CurrencyRevision(a) {
				t.Fatal("wallet projection", found, len(v.Metadata))
			}
			_, found, err = open().Account.(repository.AccountCurrencyReader).LoadAccountCurrency(ctx, "wallet:missing")
			check(err)
			if found {
				t.Fatal("missing account")
			}
		})
	}
}

func TestV1SystemPolicyCoversOfflineOperations(t *testing.T) {
	for _, family := range []string{"inventory", "quest", "rename", "adventure-rename", "container", "expert-job", "gold", "level", "mailbox", "profession", "skill-point", "vault"} {
		for _, suffix := range []string{"", "/preview"} {
			if !systemPolicyRoute("/api/v1/characters/10/" + family + "-operations" + suffix) {
				t.Fatal(family, suffix)
			}
		}
	}
	if !systemPolicyRoute("/api/v1/characters/10/mailbox-cleanups/id/run") || systemPolicyRoute("/api/v1/hub/news") {
		t.Fatal("route scope")
	}
}

func TestV1RenameInputExplainsFailure(t *testing.T) {
	for name, expected := range map[string]string{"": "填写", "七个汉字的名字": "14 字节", "A": "1 字节", "abc\n": "换行", "emoji😀": "不支持的字符"} {
		if got := characterNameErrorMessage(name); !strings.Contains(got, expected) {
			t.Fatal(name, got)
		}
	}
}
