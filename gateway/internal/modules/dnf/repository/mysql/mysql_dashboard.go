package mysql

import (
	"context"
	"database/sql"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"errors"
	"time"
)

// Read scalar aggregates from authoritative shards. Never load character blobs,
// mailbox rows or per-player histories. Older schemas omit only the clear group.
func (s *mysqlCharStore) DashboardCounts(ctx context.Context, from, to time.Time, threshold int64) (repository.DashboardCounts, error) {
	var out repository.DashboardCounts
	if !to.After(from) || to.Sub(from) > 24*time.Hour || threshold < 1 || threshold > 86400000 || len(s.router.writeDBs) == 0 {
		return out, errors.New("dashboard range invalid")
	}
	var characters, clears, fatigue, samples, fast int64
	var charErr, clearErr error
	for _, database := range s.router.writeDBs {
		var count int64
		err := s.router.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+mysqlTable(database, s.router.tablePrefix, mysqlCharTable)+" WHERE delete_flag=0").Scan(&count)
		if err != nil {
			charErr = err
		} else {
			characters += count
		}
		var runs, coverage, quick int64
		var spent sql.NullInt64
		err = s.router.db.QueryRowContext(ctx, "SELECT COUNT(*),SUM(fatigue_spent),COUNT(fatigue_spent),COALESCE(SUM(duration_ms>0 AND duration_ms<?),0) FROM "+mysqlTable(database, s.router.tablePrefix, "clear_history")+" WHERE finished_at>=? AND finished_at<?", threshold, from, to).Scan(&runs, &spent, &coverage, &quick)
		if err != nil {
			clearErr = err
		} else {
			clears += runs
			fatigue += spent.Int64
			samples += coverage
			fast += quick
		}
	}
	if charErr == nil {
		out.Characters = &characters
	}
	if clearErr == nil {
		out.Clears = &clears
		out.FastClears = &fast
		out.FatigueSamples = &samples
		if samples > 0 || clears == 0 {
			out.Fatigue = &fatigue
		}
	}
	return out, errors.Join(charErr, clearErr)
}
