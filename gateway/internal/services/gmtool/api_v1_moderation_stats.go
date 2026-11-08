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
	"time"
)

const moderationStatsReadTimeout = 8 * time.Second

// Read names and current account bans from the registration database. IP bans
// are deliberately separate: a shared IP does not make every account banned.
func (s *Server) moderationStatsAccounts(ctx context.Context, hide bool) (map[string]string, []string, error) {
	if s.db == nil {
		return nil, nil, errors.New("registration database unavailable")
	}
	query := "SELECT account_id,account,0 FROM " + quoteIdentifier(s.cfg.Table)
	if hide {
		table := quoteIdentifier(s.cfg.Table)
		// Cast the lookup value, leaving indexed columns available for ref
		// lookups while retaining case-sensitive IDs across table collations.
		query = "SELECT a.account_id,a.account,(COALESCE(p.disabled,0)<>0 OR EXISTS(SELECT 1 FROM " + quoteIdentifier(s.cfg.Table+"_bans") + " b WHERE b.kind='account' AND b.target=BINARY a.account_id AND b.revoked=0 AND (b.expires_at IS NULL OR b.expires_at>?))) FROM " + table + " a LEFT JOIN " + quoteIdentifier(s.cfg.Table+"_player_accounts") + " p ON p.account_id=BINARY a.account_id"
	}
	var args []any
	if hide {
		args = append(args, time.Now().UTC())
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	excluded := []string{}
	for rows.Next() {
		var id, name string
		var banned bool
		if err = rows.Scan(&id, &name, &banned); err != nil {
			return nil, nil, err
		}
		names[id] = name
		if banned {
			excluded = append(excluded, id)
		}
	}
	return names, excluded, rows.Err()
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
		diagnostics.Log(s.cfg.Logger, ctx, "gm-clear-stats-failed", err)
		v1Fail(w, r, 503, "stats_unavailable", "通关统计数据暂不可用", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}
