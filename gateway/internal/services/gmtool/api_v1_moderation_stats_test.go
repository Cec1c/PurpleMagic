package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestAccountStatsSortAndPagination(t *testing.T) {
	zero, seven := int64(0), int64(7)
	rows := []moderation.AccountClearStats{{AccountID: "a", Count: 2, FatigueSpent: &zero}, {AccountID: "b", Count: 3, FatigueSpent: nil}, {AccountID: "c", Count: 4, FatigueSpent: &seven}}
	names := map[string]string{"a": "alpha", "b": "beta", "c": "charlie"}
	for _, order := range []string{"asc", "desc"} {
		p := accountStatsPage(rows, names, moderation.Query{SortBy: "fatigueSpent", SortOrder: order, Limit: 3})
		if p.Rows[2].AccountID != "b" {
			t.Fatal("unknown must sort last", p)
		}
	}
	p := accountStatsPage(rows, names, moderation.Query{SortBy: "count", SortOrder: "desc", Limit: 1, Offset: 1})
	if len(p.Rows) != 1 || p.Rows[0].AccountID != "b" || !p.HasMore {
		t.Fatal(p)
	}
	p = accountStatsPage(rows, names, moderation.Query{SortBy: "account", Search: "ALP", Limit: 50})
	if len(p.Rows) != 1 || p.Rows[0].Account != "alpha" {
		t.Fatal(p)
	}
}

func TestModerationStatsMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			s.deps.Runtime = nil
			ctx := context.Background()
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.db.Exec("DELETE FROM web_accounts")
			check(err)
			for i, id := range []string{"a", "b", "c", "d"} {
				_, err = s.db.Exec("INSERT INTO web_accounts(id,account_id,account,created_at) VALUES(?,?,?,UTC_TIMESTAMP())", i+1, id, "name-"+id)
				check(err)
			}
			_, err = s.db.Exec("CREATE TABLE web_accounts_bans(kind VARCHAR(16),target VARCHAR(128),revoked BOOL,expires_at DATETIME(6))")
			check(err)
			_, err = s.db.Exec("CREATE TABLE IF NOT EXISTS web_accounts_player_accounts(account_id VARCHAR(128),disabled BOOL)")
			check(err)
			_, err = s.db.Exec("INSERT INTO web_accounts_bans VALUES('account','b',0,NULL),('account','c',0,'2000-01-01'),('account','a',1,NULL),('ip','192.0.2.1',0,NULL)")
			check(err)
			_, err = s.db.Exec("INSERT INTO web_accounts_player_accounts(account_id,disabled) VALUES('d',1)")
			check(err)
			var db string
			check(s.db.QueryRow("SELECT DATABASE()").Scan(&db))
			base := db[:len(db)-1]
			from := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
			for i := 0; i < shards; i++ {
				table := fmt.Sprintf("`%s%d`.dnf_clear_history", base, i)
				_, err = s.db.Exec("CREATE TABLE " + table + "(account_id VARCHAR(128),character_id VARCHAR(128),character_name VARCHAR(128),dungeon_id BIGINT,dungeon_name VARCHAR(128),difficulty INT,finished_at DATETIME(6),duration_ms BIGINT,fatigue_spent BIGINT NULL)")
				check(err)
				for n, v := range []struct {
					id      string
					ms      int
					fatigue any
					delta   time.Duration
				}{{"a", 10000, 6, 0}, {"a", 30000, 2, time.Minute}, {"a", 0, 5, 2 * time.Minute}, {"a", 1000, nil, time.Hour}, {"b", 2000, 9, 0}, {"c", 10000, 0, 0}, {"d", 1000, nil, 0}, {"a", 1, 99, -time.Second}, {"a", 1, 99, 2 * time.Hour}} {
					_, err = s.db.Exec("INSERT INTO "+table+" VALUES(?,?,?,?,?,?,?,?,?)", v.id, fmt.Sprintf("%d-%d", i, n), "fixture", 101, "dungeon", 1, from.Add(v.delta), v.ms, v.fatigue)
					check(err)
				}
			}
			reader := open().Character.(repository.ModerationStatsReader)
			q := moderation.Query{From: from, To: from.Add(2 * time.Hour), Limit: 50, HideBanned: true}
			check(q.NormalizeSort("account-stats"))
			names, excluded, err := s.moderationStatsAccounts(ctx, true)
			check(err)
			q.ExcludedAccountIDs = excluded
			rows, err := reader.QueryAccountClears(ctx, q)
			check(err)
			p := accountStatsPage(rows, names, q)
			if len(p.Rows) != 2 {
				t.Fatal(p)
			}
			a := p.Rows[0]
			if a.AccountID != "a" || a.Count != int64(4*shards) || a.PeakHourly != int64(3*shards) || a.FastestMS != 1000 || a.AverageMS != 13667 || *a.FatigueSpent != int64(13*shards) || a.FatigueSamples != int64(3*shards) || math.Abs(*a.FatiguePerSecond-.2) > .00001 {
				t.Fatalf("bad aggregate %#v", a)
			}
			if p.Rows[1].FatiguePerSecond == nil || *p.Rows[1].FatiguePerSecond != 0 {
				t.Fatal("free clear lost", p.Rows[1])
			}
			q.AccountID = "a"
			q.SortBy = "count"
			clears, err := reader.QueryClears(ctx, q)
			check(err)
			for _, c := range clears.Rows {
				if c.AccountID != "a" {
					t.Fatal("account filter leaked", c)
				}
			}
			q.AccountID = ""
			q.ExcludedAccountIDs = nil
			rows, err = reader.QueryAccountClears(ctx, q)
			check(err)
			if len(rows) != 4 {
				t.Fatal("all accounts", rows)
			}
			q.MinHourlyPeak = int64(3 * shards)
			rows, err = reader.QueryAccountClears(ctx, q)
			check(err)
			if len(rows) != 1 || rows[0].AccountID != "a" {
				t.Fatal("peak threshold", rows)
			}
			token, _ := s.startSession()
			url := "/api/v1/anti-cheat/account-stats?from=" + from.Format(time.RFC3339) + "&to=" + q.To.Format(time.RFC3339) + "&hideBanned=true&sortBy=account&sortOrder=asc"
			if status, out := v1TestCall(t, s, token, "GET", url); status != 200 {
				t.Fatal(status, out)
			}
			if status, _ := v1TestCall(t, s, "", "GET", url); status != 401 {
				t.Fatal(status)
			}
			if status, _ := v1TestCall(t, s, token, "GET", url+"&accountId=a&hideBanned=maybe"); status != 400 {
				t.Fatal(status)
			}
		})
	}
}
