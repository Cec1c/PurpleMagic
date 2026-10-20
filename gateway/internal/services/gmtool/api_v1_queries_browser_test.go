package gmtool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/onlineevent"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1QueriesBrowserMySQL(t *testing.T) {
	script := os.Getenv("DNF90GM_QUERIES_BROWSER_SCRIPT")
	if script == "" {
		t.Skip("query browser is opt-in")
	}
	s, _, open := currencyFixture(t, 1)
	g, ctx := open(), context.Background()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 3; i <= 20; i++ {
		_, e := s.db.Exec("INSERT INTO web_accounts(id,account_id,account) VALUES(?,?,?)", i, fmt.Sprint("wallet:", i), fmt.Sprint("账号", i))
		check(e)
		check(g.Account.Save(ctx, repository.AccountRecord{AccountID: fmt.Sprint("wallet:", i), Metadata: map[string]string{"account_cera": "1000"}}))
	}
	for slot, id := range []string{"10", "11"} {
		check(g.Character.Save(ctx, repository.CharacterRecord{CharacterID: id, AccountID: "wallet:1", Name: "查询测试" + id, Slot: slot, Job: "0", Level: 90, Stats: map[string]int64{"gold": 10000}}))
	}
	source := questFixture("浏览器任务")
	source.readonlyTestSource["equipment/weapon/test_sword.equ"] = "[name]\n`测试短剑`\n[durability]\n30\n[minimum level]\n1\n[physical attack]\n10\n"
	source.readonlyTestSource["stackable/consumption/test_potion.stk"] = "[name]\n`测试药水`\n[stack limit]\n10000\n[basic explain]\n`恢复生命值`\n"
	mode := &modeTestSource{itemCacheTestSource: newItemCacheTestSource(source.readonlyTestSource), mode: "disk"}
	s.deps.PVF = func() (dnfpvf.Source, error) { return mode, nil }
	s.pvf = newPVFCache()
	s.cfg.Logger = func(string, ...any) {}
	index := filepath.Join(t.TempDir(), "items.json")
	_, err := BuildQuestIndex(ctx, mode, index)
	check(err)
	s.items = newItemIndex(index, s, s.cfg.Logger)
	s.items.setEntriesLocked(itemcfgTestEntries())
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in adminapi.Request
		check(json.NewDecoder(r.Body).Decode(&in))
		out := adminapi.Response{Version: 1, OK: true}
		switch in.Action {
		case "activities":
			out.Activities = &onlineevent.ControlSnapshot{Revision: 1, RestartRequired: true, Activities: []onlineevent.ControlRow{{ControlDefinition: onlineevent.ControlDefinition{ID: 2395, Name: "红尾乔纳森", Description: "服务器活动"}, Enabled: true, EffectiveEnabled: false, PendingRestart: true}}}
		case "online_snapshot":
			out.OnlineSnapshot = &adminapi.OnlineSnapshot{ID: strings.Repeat("a", 32), ObservedAt: time.Now().UTC(), Complete: true, Members: []adminapi.OnlineMember{}}
		case "account_edit_status":
			out.AccountEdit = &adminapi.AccountEditStatus{Protocol: adminapi.AccountEditProtocol, AccountID: in.AccountID}
		case "character_edit_status":
			out.CharacterEdit = &adminapi.CharacterEditStatus{Protocol: adminapi.CharacterEditProtocol, AccountID: in.AccountID, CharacterID: in.CharacterID}
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer runtime.Close()
	s.deps.Runtime, err = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
	check(err)
	root, err := filepath.Abs("../../../../gm-console/dist")
	check(err)
	handler := s.ManagementHandler(root)
	var delay, fail, gets, active, peak atomic.Int64
	delay.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/control" {
			var in struct{ Delay, Fail int64 }
			check(json.NewDecoder(r.Body).Decode(&in))
			delay.Store(in.Delay)
			fail.Store(in.Fail)
			gets.Store(0)
			peak.Store(0)
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__fixture/metrics" {
			json.NewEncoder(w).Encode(map[string]int64{"gets": gets.Load(), "peak": peak.Load()})
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/") {
			gets.Add(1)
			if strings.HasSuffix(r.URL.Path, "/currency") {
				n := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); n > old; old = peak.Load() {
					if peak.CompareAndSwap(old, n) {
						break
					}
				}
				select {
				case <-time.After(time.Duration(delay.Load()) * time.Millisecond):
				case <-r.Context().Done():
					return
				}
				if strings.Contains(r.URL.Path, "wallet:1/") && fail.Load() != 0 {
					v1Fail(w, r, 503, "injected", "测试点券读取失败", true)
					return
				}
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	cmd := exec.Command("python", script, "--url", server.URL, "--output-dir", os.Getenv("DNF90GM_QUERIES_BROWSER_OUTPUT"))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("query browser: %v\n%s", e, out)
	} else {
		t.Log(string(out))
	}
	for _, id := range []string{"10", "11"} {
		q, _, e := g.Quest.Load(ctx, id)
		check(e)
		if q.States[1].Status != "completed" {
			t.Fatal("quest not completed", id, q)
		}
		c, _, e := g.Character.Load(ctx, id)
		check(e)
		if c.Stats["gold"] != 10000 {
			t.Fatal("quest granted reward", id)
		}
		rows, e := g.Character.(repository.CharacterManagementUnitOfWork).CharacterManagementLog(ctx, id, 0, 10)
		check(e)
		if len(rows) != 1 {
			t.Fatal("quest receipt count", id, len(rows))
		}
	}
}
