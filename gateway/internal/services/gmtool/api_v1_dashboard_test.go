package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestV1DashboardBounds(t *testing.T) {
	for _, c := range []struct{ now, day, week string }{
		{"2026-10-25T16:00:00Z", "2026-10-25T16:00:00Z", "2026-10-25T16:00:00Z"},
		{"2026-10-25T15:59:59Z", "2026-10-24T16:00:00Z", "2026-10-18T16:00:00Z"},
		{"2027-01-01T00:00:00Z", "2026-12-31T16:00:00Z", "2026-12-27T16:00:00Z"},
	} {
		at, _ := time.Parse(time.RFC3339, c.now)
		day, week, start := dashboardBounds(at)
		if day.Format(time.RFC3339) != c.day || week.Format(time.RFC3339) != c.week || day.Sub(start) != 363*24*time.Hour {
			t.Fatal(c, day, week, start)
		}
	}
}
func TestV1DashboardCacheAndCancellation(t *testing.T) {
	var cache dashboardCache
	var reads atomic.Int32
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	load := func(context.Context, time.Time) dashboardView {
		reads.Add(1)
		time.Sleep(20 * time.Millisecond)
		return dashboardView{Metrics: map[string]int64{"accounts": 3}}
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := cache.get(context.Background(), now, load)
			if e != nil || v.Metrics["accounts"] != 3 {
				t.Error(v, e)
			}
		}()
	}
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatal("cache stampede", reads.Load())
	}
	cache.get(context.Background(), now.Add(61*time.Second), load)
	if reads.Load() != 2 {
		t.Fatal("cache did not expire")
	}
	cache.loading = make(chan struct{})
	cache.until = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.get(ctx, now, load); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestV1DashboardAuthenticationAndPartial(t *testing.T) {
	s := &Server{www: http.NotFoundHandler(), sessions: map[string]time.Time{}, cfg: Config{Password: "fixture"}}
	if status, _ := v1TestCall(t, s, "", "GET", "/api/v1/dashboard"); status != 401 {
		t.Fatal(status)
	}
	token, _ := s.startSession()
	if status, _ := v1TestCall(t, s, token, "GET", "/api/v1/dashboard?all=1"); status != 400 {
		t.Fatal(status)
	}
	status, out := v1TestCall(t, s, token, "GET", "/api/v1/dashboard")
	if status != 200 || len(out["data"].(map[string]any)["metrics"].(map[string]any)) != 0 {
		t.Fatal(status, out)
	}
}
func TestV1DashboardMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open := currencyFixture(t, shards)
			ctx := context.Background()
			g := open()
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.db.Exec("DELETE FROM web_accounts")
			check(err)
			now := time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC)
			day, week, _ := dashboardBounds(now)
			for i, at := range []time.Time{day.Add(-time.Second), day, now.Add(time.Second), week.Add(time.Hour)} {
				_, err := s.db.Exec("INSERT INTO web_accounts(id,account_id,account,created_at,last_login_at) VALUES(?,?,?,?,?)", i+1, fmt.Sprint(i), "fixture", at, at)
				check(err)
			}
			for i := 1; i <= 4; i++ {
				check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: "wallet:1", CharacterID: fmt.Sprint(i), Name: "fixture", Slot: i, CreatedAt: day}))
			}
			check(g.Character.Save(ctx, repository.CharacterRecord{AccountID: "wallet:1", CharacterID: "5", Name: "deleted", Slot: 5, CreatedAt: day, Stats: map[string]int64{"delete_flag": 1}}))
			// Only the disposable fixture adds the existing game's table contract.
			var database string
			check(s.db.QueryRow("SELECT DATABASE()").Scan(&database))
			base := database[:len(database)-1]
			for i := 0; i < shards; i++ {
				table := fmt.Sprintf("`%s%d`.dnf_clear_history", base, i)
				_, err := s.db.Exec("CREATE TABLE " + table + "(finished_at DATETIME(6),duration_ms BIGINT,fatigue_spent BIGINT NULL,KEY(finished_at))")
				check(err)
				for _, r := range []struct {
					at      time.Time
					ms      int
					fatigue any
				}{{day, 29999, 7}, {day, 30000, 0}, {day, 0, nil}, {day.Add(-time.Second), 1, 99}, {now, 1, 99}} {
					_, err = s.db.Exec("INSERT INTO "+table+" VALUES(?,?,?)", r.at, r.ms, r.fatigue)
					check(err)
				}
			}
			out := s.readDashboard(ctx, now)
			for key, want := range map[string]int64{"accounts": 4, "characters": 4, "registrationsToday": 1, "dailyActive": 1, "weeklyActive": 3, "clearsToday": int64(3 * shards), "fatigueToday": int64(7 * shards), "fastClearsToday": int64(shards)} {
				if out.Metrics[key] != want {
					t.Fatalf("%s got %d want %d", key, out.Metrics[key], want)
				}
			}
			if len(out.Registrations) != 364 || out.Registrations[363].Count != 1 || *out.FatigueSamples != int64(2*shards) {
				t.Fatal(out)
			}
			_, err = s.db.Exec("DROP TABLE `" + database + "`.dnf_clear_history")
			check(err)
			partial := s.readDashboard(ctx, now)
			if _, ok := partial.Metrics["clearsToday"]; ok {
				t.Fatal("missing shard treated as complete")
			}
			if partial.Metrics["characters"] != 4 || partial.Metrics["accounts"] != 4 {
				t.Fatal(partial)
			}
		})
	}
}
