package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"net/http"
	"sync"
	"time"
)

var dashboardZone = time.FixedZone("Asia/Shanghai", 8*3600)

const dashboardFastThresholdMS int64 = 30000

type dashboardDay struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}
type dashboardView struct {
	ObservedAt      time.Time        `json:"observedAt"`
	Timezone        string           `json:"timezone"`
	Metrics         map[string]int64 `json:"metrics"`
	Registrations   []dashboardDay   `json:"registrations,omitempty"`
	FastThresholdMS int64            `json:"fastThresholdMs"`
	FatigueSamples  *int64           `json:"fatigueSamples,omitempty"`
}
type dashboardCache struct {
	mu      sync.Mutex
	value   *dashboardView
	until   time.Time
	loading chan struct{}
}

func dashboardBounds(now time.Time) (day, week, start time.Time) {
	local := now.In(dashboardZone)
	day = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, dashboardZone)
	week = day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	start = day.AddDate(0, 0, -363)
	return day.UTC(), week.UTC(), start.UTC()
}

// One shared cache per server; concurrent clients share the same bounded read.
func (c *dashboardCache) get(ctx context.Context, now time.Time, load func(context.Context, time.Time) dashboardView) (*dashboardView, error) {
	for {
		c.mu.Lock()
		if c.value != nil && now.Before(c.until) {
			v := c.value
			c.mu.Unlock()
			return v, nil
		}
		if c.loading != nil {
			done := c.loading
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		c.loading = make(chan struct{})
		c.mu.Unlock()
		bounded, cancel := context.WithTimeout(ctx, 4*time.Second)
		value := load(bounded, now)
		cancel()
		c.mu.Lock()
		// A failed/cancelled read is retried sooner; incomplete groups are not zeros.
		ttl := 60 * time.Second
		if len(value.Metrics) == 0 {
			ttl = 10 * time.Second
		}
		c.value = &value
		c.until = now.Add(ttl)
		if day, _, _ := dashboardBounds(now); c.until.After(day.Add(24 * time.Hour)) {
			c.until = day.Add(24 * time.Hour)
		}
		close(c.loading)
		c.loading = nil
		c.mu.Unlock()
		return &value, nil
	}
}
func (s *Server) v1Dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "仪表盘不接受查询参数", false)
		return
	}
	value, err := s.dashboard.get(r.Context(), time.Now().UTC(), s.readDashboard)
	if err != nil {
		v1Fail(w, r, 503, "dashboard_unavailable", "仪表盘暂未刷新", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: value})
}
func (s *Server) readDashboard(ctx context.Context, now time.Time) dashboardView {
	out := dashboardView{ObservedAt: now, Timezone: "Asia/Shanghai", Metrics: map[string]int64{}, FastThresholdMS: dashboardFastThresholdMS}
	day, week, start := dashboardBounds(now)
	if s.db != nil {
		var total, today, daily, weekly int64
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),COUNT(CASE WHEN created_at>=? AND created_at<? THEN 1 END),COUNT(CASE WHEN last_login_at>=? AND last_login_at<? THEN 1 END),COUNT(CASE WHEN last_login_at>=? AND last_login_at<? THEN 1 END) FROM "+quoteIdentifier(s.cfg.Table), day, now, day, now, week, now).Scan(&total, &today, &daily, &weekly)
		if err == nil {
			out.Metrics["accounts"] = total
			out.Metrics["registrationsToday"] = today
			out.Metrics["dailyActive"] = daily
			out.Metrics["weeklyActive"] = weekly
		} else {
			diagnostics.Log(s.cfg.Logger, ctx, "dashboard-registration-failed", err)
		}
		// Registration timestamps use the existing UTC database contract. A fixed
		// offset avoids requiring MySQL timezone tables for this non-DST timezone.
		rows, err := s.db.QueryContext(ctx, "SELECT DATE_FORMAT(DATE_ADD(created_at,INTERVAL 8 HOUR),'%Y-%m-%d'),COUNT(*) FROM "+quoteIdentifier(s.cfg.Table)+" WHERE created_at>=? AND created_at<? GROUP BY 1 ORDER BY 1", start, now)
		if err == nil {
			counts := map[string]int64{}
			for rows.Next() {
				var date string
				var count int64
				if err = rows.Scan(&date, &count); err != nil {
					break
				}
				counts[date] = count
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err == nil {
				for at := start; !at.After(day); at = at.AddDate(0, 0, 1) {
					date := at.In(dashboardZone).Format("2006-01-02")
					out.Registrations = append(out.Registrations, dashboardDay{date, counts[date]})
				}
			}
		}
		if err != nil {
			diagnostics.Log(s.cfg.Logger, ctx, "dashboard-heatmap-failed", err)
		}
	}
	if s.deps.Repos != nil {
		if group, ok := s.deps.Repos(); ok {
			if reader, ok := group.Character.(repository.DashboardReader); ok {
				counts, err := reader.DashboardCounts(ctx, day, now, dashboardFastThresholdMS)
				for key, value := range map[string]*int64{"characters": counts.Characters, "clearsToday": counts.Clears, "fatigueToday": counts.Fatigue, "fastClearsToday": counts.FastClears} {
					if value != nil {
						out.Metrics[key] = *value
					}
				}
				out.FatigueSamples = counts.FatigueSamples
				if err != nil {
					diagnostics.Log(s.cfg.Logger, ctx, "dashboard-game-aggregates-failed", err)
				}
			}
		}
	}
	return out
}
