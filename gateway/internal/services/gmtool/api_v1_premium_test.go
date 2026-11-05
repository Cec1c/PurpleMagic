package gmtool

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/premium"
)

func TestV1AccountPremiumMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open, dbs := currencyFixtureDatabases(t, shards)
			ctx := context.Background()
			g := open()
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			account, _, err := g.Account.Load(ctx, "wallet:1")
			must(err)
			for i := 0; i < 300; i++ {
				account.Metadata[fmt.Sprintf("unrelated_%d", i)] = strings.Repeat("x", 64)
			}
			must(g.Account.Save(ctx, account))
			before, _, err := g.Account.Load(ctx, "wallet:1")
			must(err)
			s.deps.PremiumTypes = func() ([]int64, error) { return []int64{premium.TypeBlackDiamond}, nil }
			token, err := s.startSession()
			must(err)
			for _, db := range dbs {
				for _, event := range []string{"DELETE", "INSERT", "UPDATE"} {
					row := "NEW"
					if event == "DELETE" {
						row = "OLD"
					}
					_, err = s.db.Exec(fmt.Sprintf("CREATE TRIGGER `%s`.premium_%s BEFORE %s ON `%s`.dnf_account_metadata FOR EACH ROW BEGIN IF %s.entry_key <> 'premium_expire_17' AND %s.entry_key NOT LIKE 'gm_premium_request_%%' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='unrelated metadata touched'; END IF; END", db, event, event, db, row, row))
					must(err)
				}
			}
			call := func(days int, key string) (int, map[string]any) {
				return operationCall(s, token, "accounts/wallet:1/premium-grants", fmt.Sprintf(`{"days":%d}`, days), key, nil)
			}
			for _, days := range []int{0, -1, 3651} {
				code, _ := call(days, "premium-invalid-fixture")
				if code != 422 {
					t.Fatal(code)
				}
			}
			start := time.Now().Unix()
			var wg sync.WaitGroup
			results := make(chan int, 4)
			for range 4 {
				wg.Add(1)
				go func() { defer wg.Done(); code, _ := call(30, "premium-account-fixture-1"); results <- code }()
			}
			wg.Wait()
			close(results)
			for code := range results {
				if code != 200 {
					t.Fatal("concurrent grant", code)
				}
			}
			after, _, err := open().Account.Load(ctx, "wallet:1")
			must(err)
			expiry := premium.ExpireAt(after, 17)
			if expiry < start+30*86400 || expiry > time.Now().Unix()+30*86400 {
				t.Fatal("grant repeated", expiry)
			}
			for key, value := range before.Metadata {
				if after.Metadata[key] != value {
					t.Fatal("metadata lost", key)
				}
			}
			if len(after.Metadata) != len(before.Metadata)+2 {
				t.Fatal("expected expiry and one receipt")
			}
			code, _ := call(7, "premium-account-fixture-1")
			if code != 409 {
				t.Fatal("conflicting retry", code)
			}
			// Inject failure on receipt insert: the expiry update must roll back with it.
			for _, db := range dbs {
				_, err = s.db.Exec(fmt.Sprintf("CREATE TRIGGER `%s`.premium_fail BEFORE INSERT ON `%s`.dnf_account_metadata FOR EACH ROW BEGIN IF NEW.entry_key LIKE 'gm_premium_request_%%' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='fixture receipt failure'; END IF; END", db, db))
				must(err)
			}
			code, _ = call(7, "premium-account-fixture-2")
			if code != 503 {
				t.Fatal(code)
			}
			rolled, _, err := open().Account.Load(ctx, "wallet:1")
			must(err)
			if !reflect.DeepEqual(rolled, after) {
				t.Fatal("failed grant did not roll back")
			}
			for _, db := range dbs {
				_, err = s.db.Exec("DROP TRIGGER `" + db + "`.premium_fail")
				must(err)
			}
			code, out := call(7, "premium-account-fixture-2")
			if code != 200 {
				t.Fatal(code, out)
			}
			renewed, _, err := open().Account.Load(ctx, "wallet:1")
			must(err)
			if premium.ExpireAt(renewed, 17) != expiry+7*86400 {
				t.Fatal("renewal did not extend")
			}
			code, out = v1TestCall(t, s, token, "GET", "/api/v1/accounts/wallet:1/premium")
			if code != 200 || out["data"].(map[string]any)["active"] != true {
				t.Fatal(code, out)
			}
			// An account with no character can receive the same account-wide contract.
			if code, out := operationCall(s, token, "accounts/wallet:2/premium-grants", `{"days":1}`, "premium-no-role-fixture", nil); code != 200 {
				t.Fatal(code, out)
			}
			s.deps.PremiumTypes = func() ([]int64, error) { return []int64{22}, nil }
			if code, _ := call(1, "premium-not-registered"); code != 422 {
				t.Fatal(code)
			}
		})
	}
}
