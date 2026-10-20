package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

// An actual HTTP service and disposable database with synthetic item definitions.
// The browser only hides unrelated growth capabilities absent from this fixture.
func TestV1BenefitRewardsBrowserMySQL(t *testing.T) {
	script := os.Getenv("DNF90GM_BENEFIT_REWARDS_BROWSER_SCRIPT")
	if script == "" {
		t.Skip("benefit browser is opt-in")
	}
	s, _, open := currencyFixture(t, 1)
	g, ctx := open(), context.Background()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	store, err := testbenefits.NewStore(s.db, "web_accounts")
	check(err)
	check(store.EnsureSchema(ctx))
	s.deps.Benefits = store
	source := newItemCacheTestSource(readonlyTestSource{
		"equipment/weapon/test_sword.equ":       "[durability]\n30\n",
		"stackable/consumption/test_potion.stk": "[stack limit]\n10000\n",
		itemcfgAmplifyItemPVFPath:               itemcfgTestAmplifyItemText, itemcfgSeparateUpgradePVFPath: itemcfgTestSeparateUpgradeText,
	})
	s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
	s.pvf = newPVFCache()
	s.items = newItemIndex("", s, func(string, ...any) {})
	s.items.setEntriesLocked(itemcfgTestEntries())
	for i, id := range []string{"10", "11", "20"} {
		account := "wallet:1"
		if id == "20" {
			account = "wallet:2"
		}
		check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: account, CharacterID: id, Name: "福利测试" + id, Job: "0", Level: 90, Slot: i, CreatedAt: time.Now().UTC().Add(-time.Hour)}))
	}
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(adminapi.Response{Version: 1, OK: true, OnlineSnapshot: &adminapi.OnlineSnapshot{ID: strings.Repeat("a", 32), ObservedAt: time.Now().UTC(), Complete: true, Members: []adminapi.OnlineMember{}}})
	}))
	defer runtime.Close()
	s.deps.Runtime, err = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
	check(err)
	root, err := filepath.Abs("../../../../gm-console/dist")
	check(err)
	server := httptest.NewServer(s.ManagementHandler(root))
	defer server.Close()
	// Exercise the same resumable worker used by the external GM, with a short
	// fixture-only schedule. Viewing status itself never sends rewards.
	worker, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(100 * time.Millisecond)
		defer timer.Stop()
		var position benefitScanPosition
		for {
			select {
			case <-worker.Done():
				return
			case <-timer.C:
				position, _ = s.scanBenefitDeliveries(worker, position)
			}
		}
	}()
	defer func() { stop(); <-done }()
	cmd := exec.Command("python", script, "--url", server.URL, "--output-dir", os.Getenv("DNF90GM_BENEFIT_REWARDS_BROWSER_OUTPUT"))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("benefit browser: %v\n%s", e, out)
	} else {
		t.Log(string(out))
	}
	for _, account := range []string{"wallet:1", "wallet:2"} {
		a, _, e := g.Account.Load(ctx, account)
		check(e)
		if a.Metadata["account_cera"] != "1500" {
			t.Fatal("browser did not credit exactly once", a)
		}
	}
	for _, id := range []string{"10", "11", "20"} {
		box, _, e := g.Mailbox.Load(ctx, id)
		check(e)
		found, equipment := 0, 0
		for _, mail := range box.Mails {
			for _, item := range mail.Attachments {
				if item.ItemID == 7001 && item.Count == 1 {
					equipment++
				}
				if item.ItemID == 7002 && item.Count == 12 {
					found++
				}
			}
		}
		if found != 1 || equipment != 1 {
			t.Fatal("browser item delivery", id, found, equipment)
		}
	}
	jobs, err := store.Jobs(ctx)
	check(err)
	if len(jobs) != 0 {
		t.Fatal("browser status generated a delivery job")
	}
}
