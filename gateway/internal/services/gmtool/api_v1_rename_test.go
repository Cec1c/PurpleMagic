package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type renameFailUOW struct {
	repository.CharacterRenameUnitOfWork
}

func (u renameFailUOW) WithinCharacterRename(ctx context.Context, a, id string, fn func(repository.CharacterRenameRepository, repository.ManagementReceiptStore) error) error {
	return u.CharacterRenameUnitOfWork.WithinCharacterRename(ctx, a, id, func(r repository.CharacterRenameRepository, receipts repository.ManagementReceiptStore) error {
		return fn(r, failingReceipts{receipts})
	})
}
func TestV1RenameNameRules(t *testing.T) {
	for _, name := range []string{"紫色魔法", "Abc123", "六个汉字名字"} {
		if e := adminops.ValidateCharacterName(name); e != nil {
			t.Fatal(name, e)
		}
	}
	for _, name := range []string{"", "a", "abcdefghijklM", "七个汉字的名字", "🧙", "a\x00b", "ab\n", " ab"} {
		if e := adminops.ValidateCharacterName(name); e == nil {
			t.Fatal(name)
		}
	}
}
func TestV1RenameMySQL(t *testing.T) {
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
			for i, id := range []string{"10", "11", "20"} {
				account := "wallet:1"
				if id == "20" {
					account = "wallet:2"
				}
				check(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: id, AccountID: account, Slot: i, Name: "旧名" + id, Level: 90, Job: "2", Stats: map[string]int64{"gold": 7654321, "keep": 99, "ex_equip_slot_stat": 3}}))
			}
			read := func(id string) repository.CharacterRecord {
				v, ok, e := open().Character.Load(ctx, id)
				check(e)
				if !ok {
					t.Fatal("missing", id)
				}
				return v
			}
			before, other := read("10"), read("11")
			account, _, e := g.Account.Load(ctx, "wallet:1")
			check(e)
			u := g.Character.(repository.CharacterRenameUnitOfWork)
			c := adminops.RenameCommand{AccountID: "wallet:1", CharacterID: "10", ExpectedName: before.Name, NewName: "紫色魔法", Reason: "角色改名", Actor: "管理员", Key: "rename-test-request-001"}
			offline := func(context.Context, string) error { return nil }
			_, e = adminops.RunRenameOperation(ctx, u, c, false, nil)
			check(e)
			if !reflect.DeepEqual(read("10"), before) {
				t.Fatal("preview wrote")
			}
			for _, mode := range []string{"receipt", "online", "late-online", "unavailable"} {
				worker := u
				off := offline
				switch mode {
				case "receipt":
					worker = renameFailUOW{u}
				case "online":
					off = func(context.Context, string) error { return adminops.ErrCharacterOnline }
				case "unavailable":
					off = nil
				case "late-online":
					calls := 0
					off = func(context.Context, string) error {
						calls++
						if calls == 2 {
							return adminops.ErrCharacterOnline
						}
						return nil
					}
				}
				if _, e = adminops.RunRenameOperation(ctx, worker, c, true, off); e == nil {
					t.Fatal(mode, "accepted")
				}
				if !reflect.DeepEqual(read("10"), before) {
					t.Fatal(mode, "changed record")
				}
			}
			taken := c
			taken.NewName = "旧名20"
			if _, e = adminops.RunRenameOperation(ctx, u, taken, true, offline); !errors.Is(e, repository.ErrCharacterNameTaken) {
				t.Fatal("cross-shard duplicate", e)
			}
			wrong := c
			wrong.ExpectedName = "过期名字"
			if _, e = adminops.RunRenameOperation(ctx, u, wrong, true, offline); !errors.Is(e, adminops.ErrConflict) {
				t.Fatal(e)
			}
			got, e := adminops.RunRenameOperation(ctx, u, c, true, offline)
			check(e)
			current := read("10")
			current.Name = before.Name
			current.UpdatedAt = before.UpdatedAt
			if !reflect.DeepEqual(current, before) || !reflect.DeepEqual(read("11"), other) {
				t.Fatal("unrelated character data changed")
			}
			afterAccount, _, e := g.Account.Load(ctx, "wallet:1")
			check(e)
			if !reflect.DeepEqual(account, afterAccount) {
				t.Fatal("account changed")
			}
			replay, e := adminops.RunRenameOperation(ctx, open().Character.(repository.CharacterRenameUnitOfWork), c, true, nil)
			check(e)
			if !reflect.DeepEqual(got, replay) {
				t.Fatal("receipt replay")
			}
			wrong = c
			wrong.NewName = "别的名字"
			if _, e = adminops.RunRenameOperation(ctx, u, wrong, true, offline); !errors.Is(e, adminops.ErrIdempotency) {
				t.Fatal(e)
			}
			rows, e := u.CharacterRenameLog(ctx, "10", 0, 20)
			check(e)
			if len(rows) != 1 {
				t.Fatal(len(rows))
			}
			questRows, e := g.Character.(repository.CharacterManagementUnitOfWork).CharacterManagementLog(ctx, "10", 0, 20)
			check(e)
			if len(questRows) != 0 {
				t.Fatal("rename leaked into quests")
			}
			// Two accounts racing to claim a free name must never both succeed.
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, id := range []string{"11", "20"} {
				v := read(id)
				cmd := adminops.RenameCommand{AccountID: v.AccountID, CharacterID: id, ExpectedName: v.Name, NewName: "唯一名字", Reason: "并发检查", Key: "rename-race-request-" + id}
				wg.Add(1)
				go func() { defer wg.Done(); _, e := adminops.RunRenameOperation(ctx, u, cmd, true, offline); errs <- e }()
			}
			wg.Wait()
			close(errs)
			success := 0
			for e := range errs {
				if e == nil {
					success++
				}
			}
			if success != 1 {
				t.Fatal("concurrent claims", success)
			}
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeAccountEditFixture(t, w, r, false) }))
			defer runtime.Close()
			s.deps.Runtime, e = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			check(e)
			token, e := s.startSession()
			check(e)
			body := map[string]any{"accountId": "wallet:1", "expectedName": "紫色魔法", "newName": "新的名字", "reason": "HTTP改名"}
			call := func(path, key string, headers map[string]string) (int, map[string]any) {
				b, _ := json.Marshal(body)
				return operationCall(s, token, path, string(b), key, headers)
			}
			path := "characters/10/rename-operations"
			if status, _ := call(path+"/preview", "", map[string]string{"X-CSRF-Token": ""}); status != 403 {
				t.Fatal("csrf", status)
			}
			if status, v := call(path+"/preview", "", nil); status != 200 {
				t.Fatal(status, v)
			}
			if status, _ := call(path, "", nil); status != 400 {
				t.Fatal("missing idempotency", status)
			}
			if status, v := call(path, "rename-http-request-001", nil); status != 200 {
				t.Fatal(status, v)
			}
			s.deps.Runtime = nil
			if status, v := call(path, "rename-http-request-001", nil); status != 200 {
				t.Fatal("replay without runtime", status, v)
			}
			status, v := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/rename-operations?limit=1")
			if status != 200 || len(v["data"].([]any)) != 1 || v["page"].(map[string]any)["nextCursor"] == "" {
				t.Fatal(status, v)
			}
			status, v = v1TestCall(t, s, token, "GET", "/api/v1/characters/10/quest-operations")
			if status != 200 {
				t.Fatal("quest history", status, v)
			}
		})
	}
}
