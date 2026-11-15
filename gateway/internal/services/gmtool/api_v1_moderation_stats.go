package gmtool

import (
	"cmp"
	"context"
	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	moderationStatsReadTimeout = 25 * time.Second
	moderationAccountCacheTTL  = 10 * time.Second
	statusClientClosedRequest  = 499
)

type moderationAccountEntry struct {
	AccountID string
	Account   string
	Banned    bool
	BanReason string
}

type moderationAccountSlot struct {
	entries []moderationAccountEntry
	until   time.Time
	gen     uint64
	loading chan struct{}
}

type moderationAccountCache struct {
	mu     sync.Mutex
	gen    uint64
	names  moderationAccountSlot
	bans   moderationAccountSlot
	review moderationAccountSlot
}

func (s *Server) invalidateModerationAccounts() {
	if s == nil {
		return
	}
	s.modAccounts.mu.Lock()
	s.modAccounts.gen++
	s.modAccounts.names.until = time.Time{}
	s.modAccounts.bans.until = time.Time{}
	s.modAccounts.review.until = time.Time{}
	s.modAccounts.mu.Unlock()
}

func (c *moderationAccountCache) getOrLoad(ctx context.Context, which string, load func(context.Context) ([]moderationAccountEntry, error)) ([]moderationAccountEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slotOf := func() *moderationAccountSlot {
		switch which {
		case "bans":
			return &c.bans
		case "review":
			return &c.review
		default:
			return &c.names
		}
	}
	for {
		now := time.Now()
		c.mu.Lock()
		slot := slotOf()
		if slot.entries != nil && slot.gen == c.gen && now.Before(slot.until) {
			out := slices.Clone(slot.entries)
			c.mu.Unlock()
			return out, nil
		}
		// If names-only is requested while a fresh bans or review snapshot exists, reuse it.
		if which == "names" {
			if c.bans.entries != nil && c.bans.gen == c.gen && now.Before(c.bans.until) {
				out := slices.Clone(c.bans.entries)
				c.mu.Unlock()
				return out, nil
			}
			if c.review.entries != nil && c.review.gen == c.gen && now.Before(c.review.until) {
				out := slices.Clone(c.review.entries)
				c.mu.Unlock()
				return out, nil
			}
		}
		if slot.loading != nil {
			done := slot.loading
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		slot.loading = done
		startGen := c.gen
		c.mu.Unlock()

		type loadResult struct {
			entries []moderationAccountEntry
			err     error
		}
		resCh := make(chan loadResult, 1)
		go func() {
			loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), moderationStatsReadTimeout)
			defer cancel()
			entries, err := load(loadCtx)
			c.mu.Lock()
			s := slotOf()
			s.loading = nil
			if err == nil && c.gen == startGen {
				s.entries = slices.Clone(entries)
				s.gen = startGen
				s.until = time.Now().Add(moderationAccountCacheTTL)
			}
			close(done)
			c.mu.Unlock()
			resCh <- loadResult{entries: entries, err: err}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-resCh:
			if res.err != nil {
				return nil, res.err
			}
			return slices.Clone(res.entries), nil
		}
	}
}

// Read names and current account bans from the registration database. IP bans
// are deliberately separate: a shared IP does not make every account banned.
func (s *Server) moderationStatsAccounts(ctx context.Context, hide bool) (map[string]string, []string, error) {
	if s.db == nil {
		return nil, nil, errors.New("registration database unavailable")
	}
	which := "names"
	if hide {
		which = "bans"
	}
	entries, err := s.modAccounts.getOrLoad(ctx, which, func(qctx context.Context) ([]moderationAccountEntry, error) {
		query := "SELECT account_id,account,0 FROM " + quoteIdentifier(s.cfg.Table)
		var args []any
		if hide {
			table := quoteIdentifier(s.cfg.Table)
			// Cast the lookup value, leaving indexed columns available for ref
			// lookups while retaining case-sensitive IDs across table collations.
			query = "SELECT a.account_id,a.account,(COALESCE(p.disabled,0)<>0 OR EXISTS(SELECT 1 FROM " + quoteIdentifier(s.cfg.Table+"_bans") + " b WHERE b.kind='account' AND b.target=BINARY a.account_id AND b.revoked=0 AND (b.expires_at IS NULL OR b.expires_at>?))) FROM " + table + " a LEFT JOIN " + quoteIdentifier(s.cfg.Table+"_player_accounts") + " p ON p.account_id=BINARY a.account_id"
			args = append(args, time.Now().UTC())
		}
		rows, err := s.db.QueryContext(qctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []moderationAccountEntry{}
		for rows.Next() {
			var e moderationAccountEntry
			if err = rows.Scan(&e.AccountID, &e.Account, &e.Banned); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	names := make(map[string]string, len(entries))
	excluded := []string{}
	for _, e := range entries {
		names[e.AccountID] = e.Account
		if hide && e.Banned {
			excluded = append(excluded, e.AccountID)
		}
	}
	return names, excluded, nil
}

func accountStatsPage(rows []moderation.AccountClearStats, names map[string]string, q moderation.Query) moderation.AccountClearPage {
	out := moderation.AccountClearPage{Rows: []moderation.AccountClearStats{}, From: q.From, To: q.To}
	for _, c := range rows {
		c.Account = names[c.AccountID]
		if q.Search != "" && c.AccountID != q.Search && !strings.Contains(strings.ToLower(c.Account), strings.ToLower(q.Search)) {
			continue
		}
		out.Rows = append(out.Rows, c)
	}
	slices.SortFunc(out.Rows, func(a, b moderation.AccountClearStats) int {
		var result int
		var missingA, missingB bool
		switch q.SortBy {
		case "account":
			result = cmp.Compare(a.Account, b.Account)
		case "accountId":
			result = cmp.Compare(a.AccountID, b.AccountID)
		case "count":
			result = cmp.Compare(a.Count, b.Count)
		case "fastestMs":
			missingA, missingB = a.FastestMS == 0, b.FastestMS == 0
			result = cmp.Compare(a.FastestMS, b.FastestMS)
		case "averageMs":
			missingA, missingB = a.AverageMS == 0, b.AverageMS == 0
			result = cmp.Compare(a.AverageMS, b.AverageMS)
		case "fatigueSpent":
			missingA, missingB = a.FatigueSpent == nil, b.FatigueSpent == nil
			if !missingA && !missingB {
				result = cmp.Compare(*a.FatigueSpent, *b.FatigueSpent)
			}
		case "fatiguePerSecond":
			missingA, missingB = a.FatiguePerSecond == nil, b.FatiguePerSecond == nil
			if !missingA && !missingB {
				result = cmp.Compare(*a.FatiguePerSecond, *b.FatiguePerSecond)
			}
		case "peakHourly":
			result = cmp.Compare(a.PeakHourly, b.PeakHourly)
		case "lastClearAt":
			result = a.LastClearAt.Compare(b.LastClearAt)
		}
		if missingA != missingB {
			if missingA {
				return 1
			}
			return -1
		}
		if q.SortOrder == "desc" {
			result = -result
		}
		if result == 0 {
			return cmp.Compare(a.AccountID, b.AccountID)
		}
		return result
	})
	if q.Offset >= len(out.Rows) {
		out.Rows = []moderation.AccountClearStats{}
		return out
	}
	out.Rows = out.Rows[q.Offset:]
	if len(out.Rows) > q.Limit {
		out.HasMore = true
		out.Rows = out.Rows[:q.Limit]
	}
	return out
}

func (s *Server) v1LocalModerationStats(w http.ResponseWriter, r *http.Request, q moderation.Query) {
	ctx, cancel := context.WithTimeout(r.Context(), moderationStatsReadTimeout)
	defer cancel()
	var data any
	var err error
	if s.deps.Repos != nil {
		if group, ok := s.deps.Repos(); ok {
			if reader, ok := group.Character.(repository.ModerationStatsReader); ok {
				var names map[string]string
				done := diagnostics.Start(ctx, "stats.registration")
				names, q.ExcludedAccountIDs, err = s.moderationStatsAccounts(ctx, q.HideBanned)
				done(err)
				if err == nil {
					q.AccountNames = names
					done = diagnostics.Start(ctx, "stats.aggregate")
					if r.PathValue("resource") == "account-stats" {
						var rows []moderation.AccountClearStats
						rows, err = reader.QueryAccountClears(ctx, q)
						if err == nil {
							data = accountStatsPage(rows, names, q)
						}
					} else {
						data, err = reader.QueryClears(ctx, q)
					}
					done(err)
				}
			}
		}
	}
	if data == nil || err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			v1Fail(w, r, statusClientClosedRequest, "request_canceled", "请求已取消", false)
			return
		}
		diagnostics.Log(s.cfg.Logger, ctx, "gm-clear-stats-failed", err)
		if errors.Is(err, context.DeadlineExceeded) {
			v1Fail(w, r, 503, "stats_timeout", "通关统计正在预热或查询超时，请稍后重试", true)
			return
		}
		v1Fail(w, r, 503, "stats_unavailable", "通关统计数据暂不可用", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}
