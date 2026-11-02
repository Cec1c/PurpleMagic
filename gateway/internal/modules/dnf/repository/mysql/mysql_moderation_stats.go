package mysql

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"errors"
	"strings"
	"time"
)

func (s *mysqlCharStore) clearStatsSource(q moderation.Query, characterSearch bool) (string, []any, error) {
	if len(s.router.writeDBs) == 0 {
		return "", nil, errors.New("clear history unavailable")
	}
	var parts []string
	var args []any
	for _, database := range s.router.writeDBs {
		where := "finished_at>=? AND finished_at<?"
		values := []any{q.From, q.To}
		if q.AccountID != "" {
			where += " AND account_id=?"
			values = append(values, q.AccountID)
		}
		if q.DungeonID != 0 {
			where += " AND dungeon_id=?"
			values = append(values, q.DungeonID)
		}
		if len(q.ExcludedAccountIDs) > 0 {
			where += " AND account_id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.ExcludedAccountIDs)), ",") + ")"
			for _, id := range q.ExcludedAccountIDs {
				values = append(values, id)
			}
		}
		if characterSearch && q.Search != "" {
			where += " AND (character_id=? OR account_id=? OR LOCATE(?,character_name)>0 OR LOCATE(?,dungeon_name)>0)"
			values = append(values, q.Search, q.Search, q.Search, q.Search)
		}
		parts = append(parts, "SELECT account_id,character_id,character_name,dungeon_id,dungeon_name,difficulty,finished_at,duration_ms,fatigue_spent FROM "+mysqlTable(database, s.router.tablePrefix, "clear_history")+" WHERE "+where)
		args = append(args, values...)
	}
	return strings.Join(parts, " UNION ALL "), args, nil
}

func (s *mysqlCharStore) QueryClears(ctx context.Context, q moderation.Query) (moderation.ClearPage, error) {
	out := moderation.ClearPage{Rows: []moderation.ClearStats{}}
	if err := q.Normalize(time.Now()); err != nil {
		return out, err
	}
	if err := q.NormalizeSort("clears"); err != nil {
		return out, err
	}
	out.From, out.To = q.From, q.To
	source, args, err := s.clearStatsSource(q, true)
	if err != nil {
		return out, err
	}
	source = "SELECT *,COUNT(*) OVER(PARTITION BY character_id,dungeon_id,difficulty,DATE_FORMAT(finished_at,'%Y-%m-%d %H')) AS hourly FROM (" + source + ") source_clears"
	columns := map[string]string{"accountId": "account_id", "characterId": "character_id", "characterName": "MAX(character_name)", "dungeonId": "dungeon_id", "dungeonName": "MAX(dungeon_name)", "difficulty": "difficulty", "count": "COUNT(*)", "fastestMs": "MIN(NULLIF(duration_ms,0))", "averageMs": "AVG(NULLIF(duration_ms,0))", "peakHourly": "MAX(hourly)", "lastClearAt": "MAX(finished_at)", "fatigueSpent": "SUM(fatigue_spent)", "averageFatigue": "AVG(fatigue_spent)"}
	column := columns[q.SortBy]
	order := "(" + column + " IS NULL) ASC," + column + " " + q.SortOrder + ",character_id,dungeon_id,difficulty,account_id"
	query := "SELECT account_id,character_id,MAX(character_name),dungeon_id,MAX(dungeon_name),difficulty,COUNT(*),MAX(finished_at),COALESCE(MIN(NULLIF(duration_ms,0)),0),COALESCE(CAST(AVG(NULLIF(duration_ms,0)) AS SIGNED),0),MAX(hourly),SUM(fatigue_spent),AVG(fatigue_spent),COUNT(fatigue_spent) FROM (" + source + ") clears GROUP BY account_id,character_id,dungeon_id,difficulty HAVING (?=0 OR MIN(NULLIF(duration_ms,0))<=?) AND (?=0 OR MAX(hourly)>=?) ORDER BY " + order + " LIMIT ? OFFSET ?"
	args = append(args, q.MaxDurationMS, q.MaxDurationMS, q.MinHourlyPeak, q.MinHourlyPeak, q.Limit+1, q.Offset)
	rows, err := s.router.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c moderation.ClearStats
		if err = rows.Scan(&c.AccountID, &c.CharacterID, &c.CharacterName, &c.DungeonID, &c.DungeonName, &c.Difficulty, &c.Count, &c.LastClearAt, &c.FastestMS, &c.AverageMS, &c.PeakHourly, &c.FatigueSpent, &c.AverageFatigue, &c.FatigueSamples); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, c)
	}
	if len(out.Rows) > q.Limit {
		out.HasMore = true
		out.Rows = out.Rows[:q.Limit]
	}
	return out, rows.Err()
}

func (s *mysqlCharStore) QueryAccountClears(ctx context.Context, q moderation.Query) ([]moderation.AccountClearStats, error) {
	if err := q.Normalize(time.Now()); err != nil {
		return nil, err
	}
	if err := q.NormalizeSort("account-stats"); err != nil {
		return nil, err
	}
	source, args, err := s.clearStatsSource(q, false)
	if err != nil {
		return nil, err
	}
	// Collapse each account's hour across ALL characters and write shards first.
	// Rate numerator and denominator use the same sampled, positive-duration runs.
	hours := "SELECT account_id,COUNT(*) AS runs,MIN(NULLIF(duration_ms,0)) AS fastest,SUM(NULLIF(duration_ms,0)) AS duration,COUNT(NULLIF(duration_ms,0)) AS timed,MAX(finished_at) AS last_clear,SUM(fatigue_spent) AS fatigue,COUNT(fatigue_spent) AS sampled,SUM(CASE WHEN duration_ms>0 THEN fatigue_spent END) AS rate_fatigue,SUM(CASE WHEN fatigue_spent IS NOT NULL AND duration_ms>0 THEN duration_ms END) AS rate_duration FROM (" + source + ") clears GROUP BY account_id,DATE_FORMAT(finished_at,'%Y-%m-%d %H')"
	query := "SELECT account_id,SUM(runs),COALESCE(MIN(fastest),0),COALESCE(CAST(SUM(duration)/NULLIF(SUM(timed),0) AS SIGNED),0),SUM(fatigue),SUM(sampled),SUM(rate_fatigue)*1000.0/NULLIF(SUM(rate_duration),0),MAX(runs),MAX(last_clear) FROM (" + hours + ") hours GROUP BY account_id HAVING (?=0 OR MIN(fastest)<=?) AND (?=0 OR MAX(runs)>=?)"
	args = append(args, q.MaxDurationMS, q.MaxDurationMS, q.MinHourlyPeak, q.MinHourlyPeak)
	rows, err := s.router.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []moderation.AccountClearStats{}
	for rows.Next() {
		var c moderation.AccountClearStats
		if err = rows.Scan(&c.AccountID, &c.Count, &c.FastestMS, &c.AverageMS, &c.FatigueSpent, &c.FatigueSamples, &c.FatiguePerSecond, &c.PeakHourly, &c.LastClearAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
