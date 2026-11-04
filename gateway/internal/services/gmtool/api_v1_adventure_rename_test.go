package gmtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type adventureFailUOW struct {
	repository.AdventureRenameUnitOfWork
}

func (u adventureFailUOW) WithinAdventureRename(ctx context.Context, id string, fn func(repository.AdventureRenameRepository, repository.ManagementReceiptStore) error) error {
	return u.AdventureRenameUnitOfWork.WithinAdventureRename(ctx, id, func(r repository.AdventureRenameRepository, receipts repository.ManagementReceiptStore) error {
		return fn(r, failingReceipts{receipts})
	})
}

func TestV1AdventureNameRules(t *testing.T) {
	for _, name := range []string{"a", "紫色冒险团", "八个常用汉字名称", "abcdefghijklmnop"} {
		if e := adminops.ValidateAdventureName(name); e != nil {
			t.Fatal(name, e)
		}
	}
	for _, name := range []string{"", "12345678901234567", "九个常用汉字的名称", " ab", "a b", "a\tb", "a\x00b", "a\u3000b", string([]byte{0xff})} {
		if e := adminops.ValidateAdventureName(name); e == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
}

func TestV1AdventureRenameMySQL(t *testing.T) {
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
			read := func(id string) repository.AccountRecord {
				v, ok, e := open().Account.Load(ctx, id)
				check(e)
				if !ok {
					t.Fatal("missing account")
				}
				return v
			}
			for i, id := range []string{"wallet:1", "wallet:2"} {
				a := read(id)
				a.RepresentAccountName = fmt.Sprintf("OldTeam%d", i)
				a.HonorExp = 987654
				a.State = "active"
				for j := 0; j < 270; j++ {
					a.Metadata[fmt.Sprintf("keep-%d", j)] = fmt.Sprint(j)
				}
				a.Metadata["adventure_group_created_date"] = "2026-01-01"
				a.Metadata["account_fate"] = "permanent-ledger"
				a.Metadata["account_cera_sequence"] = "41"
				check(g.Account.Save(ctx, a))
			}
			check(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "角色甲", Level: 90, Stats: map[string]int64{"gold": 12345}}))
			charBefore, _, e := g.Character.Load(ctx, "10")
			check(e)
			before, other := read("wallet:1"), read("wallet:2")
			u := g.Account.(repository.AdventureRenameUnitOfWork)
			c := adminops.AdventureRenameCommand{AccountID: "wallet:1", ExpectedName: before.RepresentAccountName, NewName: "紫色冒险团", Reason: "改名", Actor: "管理员", Key: "adventure-rename-001"}
			offline := func(context.Context, string) error { return nil }
			_, e = adminops.RunAdventureRename(ctx, u, c, false, nil)
			check(e)
			if !reflect.DeepEqual(read(c.AccountID), before) {
				t.Fatal("preview wrote account")
			}
			for _, mode := range []string{"online", "late-online", "receipt", "unavailable"} {
				worker := u
				off := offline
				switch mode {
				case "online":
					off = func(context.Context, string) error { return adminops.ErrCharacterOnline }
				case "late-online":
					calls := 0
					off = func(context.Context, string) error {
						calls++
						if calls == 2 {
							return adminops.ErrCharacterOnline
						}
						return nil
					}
				case "receipt":
					worker = adventureFailUOW{u}
				case "unavailable":
					off = nil
				}
				if _, e = adminops.RunAdventureRename(ctx, worker, c, true, off); e == nil {
					t.Fatal(mode, "accepted")
				}
				if !reflect.DeepEqual(read(c.AccountID), before) {
					t.Fatal(mode, "changed account")
				}
				rows, e := u.AdventureRenameLog(ctx, c.AccountID, 0, 20)
				check(e)
				if len(rows) != 0 {
					t.Fatal(mode, "left receipt")
				}
			}
			wrong := c
			wrong.ExpectedName = "旧的快照"
			if _, e = adminops.RunAdventureRename(ctx, u, wrong, true, offline); !errors.Is(e, adminops.ErrConflict) {
				t.Fatal(e)
			}
			wrong = c
			wrong.NewName = strings.ToLower(other.RepresentAccountName)
			if _, e = adminops.RunAdventureRename(ctx, u, wrong, true, offline); !errors.Is(e, repository.ErrRepresentAccountNameExists) {
				t.Fatal("database collation/cross-shard duplicate", e)
			}
			got, e := adminops.RunAdventureRename(ctx, u, c, true, offline)
			check(e)
			after := read(c.AccountID)
			if after.RepresentAccountName != c.NewName {
				t.Fatal(after.RepresentAccountName)
			}
			after.RepresentAccountName = before.RepresentAccountName
			after.UpdatedAt = before.UpdatedAt
			if !reflect.DeepEqual(after, before) || !reflect.DeepEqual(read(other.AccountID), other) {
				t.Fatal("account data changed")
			}
			charAfter, _, e := g.Character.Load(ctx, "10")
			check(e)
			if !reflect.DeepEqual(charBefore, charAfter) {
				t.Fatal("character changed")
			}
			replayed, e := adminops.RunAdventureRename(ctx, open().Account.(repository.AdventureRenameUnitOfWork), c, true, nil)
			check(e)
			if !reflect.DeepEqual(got, replayed) {
				t.Fatal("replay mismatch")
			}
			wrong = c
			wrong.NewName = "Different"
			if _, e = adminops.RunAdventureRename(ctx, u, wrong, true, offline); !errors.Is(e, adminops.ErrIdempotency) {
				t.Fatal(e)
			}
			currency, e := g.Account.(repository.AccountManagementUnitOfWork).AccountManagementLog(ctx, c.AccountID, 0, 20)
			check(e)
			if len(currency) != 0 {
				t.Fatal("rename leaked into currency history")
			}
			// A pending login waits on the same account row held by the GM writer.
			check(u.WithinAdventureRename(ctx, c.AccountID, func(_ repository.AdventureRenameRepository, _ repository.ManagementReceiptStore) error {
				wait, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				if e := open().Account.(repository.AccountEditFence).WaitForAccountEdits(wait, c.AccountID); !errors.Is(e, context.DeadlineExceeded) {
					t.Fatalf("login fence: %v", e)
				}
				return nil
			}))
			// Competing accounts, even on different shards, cannot claim one name.
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, id := range []string{"wallet:1", "wallet:2"} {
				a := read(id)
				cmd := c
				cmd.AccountID = id
				cmd.ExpectedName = a.RepresentAccountName
				cmd.NewName = "UniqueTeam"
				cmd.Key = "adventure-race-" + id
				wg.Add(1)
				go func() { defer wg.Done(); _, e := adminops.RunAdventureRename(ctx, u, cmd, true, offline); results <- e }()
			}
			wg.Wait()
			close(results)
			success := 0
			for e := range results {
				if e == nil {
					success++
				}
			}
			if success != 1 {
				t.Fatal("concurrent claims", success)
			}
			var busy atomic.Bool
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeAccountEditFixture(t, w, r, busy.Load()) }))
			defer runtime.Close()
			s.deps.Runtime, e = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			check(e)
			token, e := s.startSession()
			check(e)
			path := "accounts/wallet:1/adventure-rename-operations"
			body := map[string]any{"expectedName": read("wallet:1").RepresentAccountName, "newName": "HTTP新冒险团", "reason": "HTTP改名"}
			call := func(path, key string, headers map[string]string) (int, map[string]any) {
				b, _ := json.Marshal(body)
				return operationCall(s, token, path, string(b), key, headers)
			}
			if status, _ := call(path+"/preview", "", map[string]string{"X-CSRF-Token": ""}); status != 403 {
				t.Fatal("csrf", status)
			}
			if status, v := call(path+"/preview", "", nil); status != 200 {
				t.Fatal(status, v)
			}
			if status, _ := call(path, "", nil); status != 400 {
				t.Fatal("idempotency required", status)
			}
			busy.Store(true)
			if status, v := call(path, "adventure-http-request-001", nil); status != 409 || v["error"].(map[string]any)["code"] != "account_online" {
				t.Fatal(status, v)
			}
			busy.Store(false)
			if status, v := call(path, "adventure-http-request-001", nil); status != 200 {
				t.Fatal(status, v)
			}
			s.deps.Runtime = nil
			if status, v := call(path, "adventure-http-request-001", nil); status != 200 {
				t.Fatal("replay without runtime", status, v)
			}
			status, v := v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:1/adventure-name")
			if status != 200 || v["data"].(map[string]any)["name"] != "HTTP新冒险团" {
				t.Fatal(status, v)
			}
			status, v = v1TestCall(t, s, token, "GET", "/api/v1/"+path+"?limit=1")
			if status != 200 || len(v["data"].([]any)) != 1 {
				t.Fatal(status, v)
			}
			cursor := v["page"].(map[string]any)["nextCursor"].(string)
			if cursor == "" {
				t.Fatal("missing cursor")
			}
			status, v = v1TestCall(t, s, token, "GET", "/api/v1/"+path+"?limit=1&cursor="+url.QueryEscape(cursor))
			if status != 200 || len(v["data"].([]any)) != 1 {
				t.Fatal(status, v)
			}
			if status, _ = v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:2/adventure-rename-operations?limit=1&cursor="+url.QueryEscape(cursor)); status != 400 {
				t.Fatal("cross-account cursor", status)
			}
			if status, _ = v1TestCall(t, s, "", "GET", "/api/v1/"+path); status != 401 {
				t.Fatal("anonymous", status)
			}
			delete(body, "expectedName")
			if status, _ = call(path+"/preview", "", nil); status != 400 {
				t.Fatal("missing expected name", status)
			}
			// A registered account without an adventure is not created by this rename.
			empty := read("wallet:2")
			empty.RepresentAccountName = ""
			check(g.Account.Save(ctx, empty))
			wrong = c
			wrong.AccountID = "wallet:2"
			wrong.ExpectedName = ""
			wrong.Key = "adventure-empty-001"
			if _, e = adminops.RunAdventureRename(ctx, u, wrong, true, offline); !errors.Is(e, adminops.ErrAdventureNotRegistered) {
				t.Fatal(e)
			}
		})
	}
}
