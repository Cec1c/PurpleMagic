package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type fakeClearDBRow struct {
	shard          string
	accountID      string
	characterID    string
	characterName  string
	dungeonID      int64
	dungeonName    string
	difficulty     int64
	finishedAt     time.Time
	durationMS     int64
	fatigue        any
	completionHash string
}

type fakeClearWindowCall struct {
	query string
	from  time.Time
	to    time.Time
}

type fakeClearSQLState struct {
	mu                sync.Mutex
	hasCompletionHash bool
	hasClearTimeIndex bool
	rows              []fakeClearDBRow
	windowCalls       []fakeClearWindowCall
	charTotal         int64
}

type fakeClearConnector struct{ s *fakeClearSQLState }

func (c fakeClearConnector) Connect(context.Context) (driver.Conn, error) {
	return fakeClearConn{c.s}, nil
}
func (c fakeClearConnector) Driver() driver.Driver { return fakeClearDriver{c.s} }

type fakeClearDriver struct{ s *fakeClearSQLState }

func (d fakeClearDriver) Open(string) (driver.Conn, error) { return fakeClearConn{d.s}, nil }

type fakeClearConn struct{ s *fakeClearSQLState }

func (c fakeClearConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected prepare")
}
func (c fakeClearConn) Close() error              { return nil }
func (c fakeClearConn) Begin() (driver.Tx, error) { return fakeClearTx{}, nil }

type fakeClearTx struct{}

func (fakeClearTx) Commit() error   { return nil }
func (fakeClearTx) Rollback() error { return nil }

func (c fakeClearConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()

	if strings.HasPrefix(q, "SHOW COLUMNS FROM ") {
		if c.s.hasCompletionHash {
			return &fakeClearRows{cols: 1, vals: [][]driver.Value{{"completion_hash"}}}, nil
		}
		return &fakeClearRows{cols: 1}, nil
	}
	if strings.HasPrefix(q, "SHOW INDEX FROM ") {
		if c.s.hasClearTimeIndex {
			return &fakeClearRows{cols: 1, vals: [][]driver.Value{{"clear_time"}}}, nil
		}
		return &fakeClearRows{cols: 1}, nil
	}
	if strings.HasPrefix(q, "SELECT COUNT(*) FROM ") && strings.Contains(q, "characters") {
		return &fakeClearRows{cols: 1, vals: [][]driver.Value{{c.s.charTotal}}}, nil
	}
	if strings.Contains(q, "clear_history") && len(args) == 2 {
		from, _ := args[0].Value.(time.Time)
		to, _ := args[1].Value.(time.Time)
		c.s.windowCalls = append(c.s.windowCalls, fakeClearWindowCall{query: q, from: from, to: to})
		var matched [][]driver.Value
		for _, r := range c.s.rows {
			if !strings.Contains(q, "`"+r.shard+"`.") {
				continue
			}
			if (r.finishedAt.Equal(from) || r.finishedAt.After(from)) && r.finishedAt.Before(to) {
				row := []driver.Value{
					r.accountID,
					r.characterID,
					r.characterName,
					r.dungeonID,
					r.dungeonName,
					r.difficulty,
					r.finishedAt,
					r.durationMS,
					r.fatigue,
				}
				if c.s.hasCompletionHash {
					row = append(row, r.completionHash)
				}
				matched = append(matched, row)
			}
		}
		cols := 9
		if c.s.hasCompletionHash {
			cols = 10
		}
		return &fakeClearRows{cols: cols, vals: matched}, nil
	}
	return nil, fmt.Errorf("unexpected query: %s", q)
}

type fakeClearRows struct {
	cols int
	idx  int
	vals [][]driver.Value
}

func (r *fakeClearRows) Columns() []string {
	out := make([]string, r.cols)
	for i := range out {
		out[i] = fmt.Sprintf("c%d", i)
	}
	return out
}
func (r *fakeClearRows) Close() error { return nil }
func (r *fakeClearRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.idx])
	r.idx++
	return nil
}

func TestClearProjectionNewestFirstWarmupSnapshotAndQueries(t *testing.T) {
	now := time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)
	currentNow := now
	state := &fakeClearSQLState{
		hasCompletionHash: true,
		hasClearTimeIndex: true,
		charTotal:         42,
		rows: []fakeClearDBRow{
			// Account "acc1": 3 clears in [17:00..18:00), one with duration<=0, one with NULL fatigue.
			{shard: "s0", accountID: "acc1", characterID: "c1", characterName: "剑魂一号", dungeonID: 101, dungeonName: "グランプロリス", difficulty: 2, finishedAt: now.Add(-50 * time.Minute), durationMS: 10000, fatigue: int64(6), completionHash: "h1"},
			{shard: "s0", accountID: "acc1", characterID: "c1", characterName: "剑魂一号", dungeonID: 101, dungeonName: "グランプロリス", difficulty: 2, finishedAt: now.Add(-40 * time.Minute), durationMS: 30000, fatigue: int64(2), completionHash: "h2"},
			{shard: "s1", accountID: "acc1", characterID: "c2", characterName: "阿修罗", dungeonID: 101, dungeonName: "グランプロリス", difficulty: 2, finishedAt: now.Add(-30 * time.Minute), durationMS: -5, fatigue: int64(5), completionHash: "h3"},
			{shard: "s1", accountID: "acc1", characterID: "c2", characterName: "阿修罗", dungeonID: 102, dungeonName: "時間之門", difficulty: 1, finishedAt: now.Add(-10 * time.Minute), durationMS: 1000, fatigue: nil, completionHash: "h4"},
			// Duplicate primary key (same shard + character_id + completion_hash) must be deduplicated idempotently.
			{shard: "s1", accountID: "acc1", characterID: "c2", characterName: "阿修罗", dungeonID: 102, dungeonName: "時間之門", difficulty: 1, finishedAt: now.Add(-10 * time.Minute), durationMS: 1000, fatigue: nil, completionHash: "h4"},
			// Banned account "banned1".
			{shard: "s0", accountID: "banned1", characterID: "cb", characterName: "外挂号", dungeonID: 101, dungeonName: "グランプロリス", difficulty: 3, finishedAt: now.Add(-20 * time.Minute), durationMS: 500, fatigue: int64(8), completionHash: "hb"},
			// Free clear (0 fatigue, timed) on "acc2".
			{shard: "s0", accountID: "acc2", characterID: "c3", characterName: "奶爸", dungeonID: 201, dungeonName: "安徒恩", difficulty: 0, finishedAt: now.Add(-5 * time.Hour), durationMS: 20000, fatigue: int64(0), completionHash: "h5"},
			// Old clear 10 hours ago on "acc3" with purely negative/zero duration (untimed -> NULL average/fastest).
			{shard: "s1", accountID: "acc3", characterID: "c4", characterName: "漫游", dungeonID: 202, dungeonName: "卢克", difficulty: 1, finishedAt: now.Add(-10 * time.Hour), durationMS: 0, fatigue: nil, completionHash: "h6"},
		},
	}

	rawDB := sql.OpenDB(fakeClearConnector{s: state})
	defer rawDB.Close()

	snapPath := filepath.Join(t.TempDir(), "clear_projection.bin")
	group, err := NewMySQLGroup(SQLDBAdapter{DB: rawDB}, MySQLGroupOptions{
		DatabasePlan: repository.DatabasePlan{
			ReadDatabases:  []string{"s0", "s1"},
			WriteDatabases: []string{"s0", "s1"},
		},
		Now: func() time.Time { return currentNow },
		ClearProjection: &ClearProjectionConfig{
			SnapshotPath: snapPath,
			Retention:    12 * time.Hour,
			SliceWindow:  4 * time.Hour,
			SlicePause:   -1, // zero sleep in unit test
			TailOverlap:  5 * time.Second,
			TailMinGap:   3 * time.Second,
			BgInterval:   time.Hour,
			SaveInterval: time.Hour,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	proj := ClearProjectionFromGroup(group)
	if proj == nil {
		t.Fatal("expected ClearProjectionFromGroup to return active projection")
	}

	ctx := context.Background()
	// Cold syncTail pulls the newest 4h slice [now-4h, now+1s) first, then backfillDownTo walks backwards!
	if err := proj.syncTail(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := proj.backfillDownTo(ctx, now.Add(-12*time.Hour), 0); err != nil {
		t.Fatal(err)
	}

	// Verify Newest-First slice order and FORCE INDEX (clear_time) usage.
	state.mu.Lock()
	calls := append([]fakeClearWindowCall(nil), state.windowCalls...)
	state.mu.Unlock()
	if len(calls) != 6 { // 3 windows * 2 shards
		t.Fatalf("expected 6 shard window queries (3 slices x 2 shards), got %d", len(calls))
	}
	for _, c := range calls {
		if !strings.Contains(c.query, "FORCE INDEX (clear_time)") || !strings.Contains(c.query, "completion_hash") {
			t.Fatalf("expected FORCE INDEX (clear_time) and completion_hash in query: %s", c.query)
		}
	}
	// Slice 0: [14:00, 18:00+1s), Slice 1: [10:00, 14:00), Slice 2: [06:00, 10:00).
	if !calls[0].from.Equal(now.Add(-4*time.Hour)) || !calls[2].from.Equal(now.Add(-8*time.Hour)) || !calls[4].from.Equal(now.Add(-12*time.Hour)) {
		t.Fatalf("slices were not backfilled Newest-First: %+v", calls)
	}

	covFrom, wm, totalRecords := proj.Coverage()
	if !covFrom.Equal(now.Add(-12*time.Hour)) || !wm.Equal(now) || totalRecords != 7 {
		t.Fatalf("unexpected coverage (%v..%v, count=%d)", covFrom, wm, totalRecords)
	}

	// Verify QueryAccountClears parity (hideBanned, AverageMS rounding, FatiguePerSecond, PeakHourly, duration<=0 tolerance).
	statsReader := group.Character.(repository.ModerationStatsReader)
	q := moderation.Query{
		From:               now.Add(-12 * time.Hour),
		To:                 now,
		HideBanned:         true,
		ExcludedAccountIDs: []string{"banned1"},
		Limit:              50,
	}
	accRows, err := statsReader.QueryAccountClears(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(accRows) != 3 {
		t.Fatalf("expected 3 unbanned accounts, got %d: %+v", len(accRows), accRows)
	}
	byAcc := map[string]moderation.AccountClearStats{}
	for _, r := range accRows {
		byAcc[r.AccountID] = r
	}
	a1 := byAcc["acc1"]
	// acc1: 4 clears total; 3 timed (10000 + 30000 + 1000 = 41000ms -> round(41000/3)=13667ms);
	// fastest=1000ms; fatigueSpent=6+2+5=13 across 3 samples; timed+fatigue: (10s,6)+(30s,2) -> 8 / 40s = 0.2/s;
	// all 4 clears are inside UTC hour 17:xx -> PeakHourly=4!
	if a1.Count != 4 || a1.FastestMS != 1000 || a1.AverageMS != 13667 || a1.PeakHourly != 4 || a1.FatigueSpent == nil || *a1.FatigueSpent != 13 || a1.FatigueSamples != 3 || a1.FatiguePerSecond == nil || math.Abs(*a1.FatiguePerSecond-0.2) > 1e-6 {
		t.Fatalf("acc1 aggregate mismatch: %+v", a1)
	}
	a2 := byAcc["acc2"]
	if a2.FatiguePerSecond == nil || *a2.FatiguePerSecond != 0 {
		t.Fatalf("acc2 zero-fatigue clear lost: %+v", a2)
	}
	a3 := byAcc["acc3"]
	if a3.FastestMS != 0 || a3.AverageMS != 0 || a3.FatigueSpent != nil {
		t.Fatalf("acc3 untimed null-fatigue clear mismatch: %+v", a3)
	}

	// Verify QueryClears filtering by AccountNames search + NULLs-last sorting + pagination.
	cq := moderation.Query{
		From:               now.Add(-12 * time.Hour),
		To:                 now,
		HideBanned:         true,
		ExcludedAccountIDs: []string{"banned1"},
		AccountNames:       map[string]string{"acc1": "AlphaLogin", "acc2": "BetaLogin", "acc3": "GammaLogin"},
		SortBy:             "fastestMs",
		SortOrder:          "asc",
		Limit:              2,
		Offset:             0,
	}
	cPage, err := statsReader.QueryClears(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}
	if len(cPage.Rows) != 2 || !cPage.HasMore || cPage.Rows[0].FastestMS != 1000 || cPage.Rows[1].FastestMS != 10000 {
		t.Fatalf("unexpected fastestMs asc page 1: %+v", cPage)
	}
	cq.Offset = 2
	cPage2, err := statsReader.QueryClears(ctx, cq)
	if err != nil {
		t.Fatal(err)
	}
	// Remaining 2 groups: acc2 (20000ms) then untimed group(s) with FastestMS==0 sorted last!
	if len(cPage2.Rows) != 2 || cPage2.Rows[len(cPage2.Rows)-1].FastestMS != 0 {
		t.Fatalf("untimed group must sort last: %+v", cPage2)
	}

	// Search by AccountNames login string ("betalogin") in QueryClears.
	cqSearch := moderation.Query{
		From:         now.Add(-12 * time.Hour),
		To:           now,
		Search:       "betalogin",
		AccountNames: map[string]string{"acc1": "AlphaLogin", "acc2": "BetaLogin"},
		SortBy:       "count",
		SortOrder:    "desc",
		Limit:        10,
	}
	searchRes, err := statsReader.QueryClears(ctx, cqSearch)
	if err != nil || len(searchRes.Rows) != 1 || searchRes.Rows[0].AccountID != "acc2" {
		t.Fatalf("QueryClears AccountNames search failed: %+v, err=%v", searchRes, err)
	}

	// Verify DashboardCounts reads from memory without hitting clear_history SQL again!
	state.mu.Lock()
	beforeDashCalls := len(state.windowCalls)
	state.mu.Unlock()
	dashReader := group.Character.(repository.DashboardReader)
	dash, err := dashReader.DashboardCounts(ctx, now.Add(-2*time.Hour), now, 30000)
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	afterDashCalls := len(state.windowCalls)
	state.mu.Unlock()
	if afterDashCalls != beforeDashCalls {
		t.Fatalf("DashboardCounts hit clear_history SQL despite in-memory coverage (%d -> %d)", beforeDashCalls, afterDashCalls)
	}
	// In [now-2h, now): 5 rows (4 on acc1 + 1 on banned1); fast (<30000 & >0): 10000ms, 1000ms, 500ms => 3!
	// Fatigue sum: 6+2+5+8 = 21 across 4 samples; characters: 42*2 shards = 84.
	if dash.Characters == nil || *dash.Characters != 84 || dash.Clears == nil || *dash.Clears != 5 || dash.FastClears == nil || *dash.FastClears != 3 || dash.Fatigue == nil || *dash.Fatigue != 21 || dash.FatigueSamples == nil || *dash.FatigueSamples != 4 {
		t.Fatalf("DashboardCounts mismatch: %+v", dash)
	}

	// Save atomic snapshot, recover into a brand-new ClearHistoryProjection, and confirm ZERO historical slice SQL!
	if err := proj.SaveSnapshot(snapPath); err != nil {
		t.Fatal(err)
	}
	// Advance clock by 10s and append one incremental clear row.
	currentNow = now.Add(10 * time.Second)
	state.mu.Lock()
	state.rows = append(state.rows, fakeClearDBRow{
		shard: "s0", accountID: "acc1", characterID: "c1", characterName: "剑魂一号",
		dungeonID: 101, dungeonName: "グランプロリス", difficulty: 2,
		finishedAt: now.Add(5 * time.Second), durationMS: 8000, fatigue: int64(4), completionHash: "h_inc",
	})
	state.windowCalls = nil
	state.mu.Unlock()

	recoveredGroup, err := NewMySQLGroup(SQLDBAdapter{DB: rawDB}, MySQLGroupOptions{
		DatabasePlan: repository.DatabasePlan{
			ReadDatabases:  []string{"s0", "s1"},
			WriteDatabases: []string{"s0", "s1"},
		},
		Now: func() time.Time { return currentNow },
		ClearProjection: &ClearProjectionConfig{
			SnapshotPath: snapPath,
			Retention:    13 * time.Hour,
			SliceWindow:  4 * time.Hour,
			SlicePause:   -1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveredProj := ClearProjectionFromGroup(recoveredGroup)
	if err := recoveredProj.LoadSnapshot(snapPath); err != nil {
		t.Fatal(err)
	}
	// Querying [now-12h, currentNow) should only sync the tiny 15s incremental tail [now-5s, currentNow+1s), NOT re-scan 12h!
	recReader := recoveredGroup.Character.(repository.ModerationStatsReader)
	recRows, err := recReader.QueryAccountClears(ctx, moderation.Query{
		From:  now.Add(-12 * time.Hour),
		To:    currentNow,
		Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	tailCalls := append([]fakeClearWindowCall(nil), state.windowCalls...)
	state.mu.Unlock()
	if len(tailCalls) != 2 || !tailCalls[0].from.Equal(now.Add(-5*time.Second)) {
		t.Fatalf("expected only incremental tail query from watermark-5s, got %+v", tailCalls)
	}
	foundUpdatedAcc1 := false
	for _, r := range recRows {
		if r.AccountID == "acc1" && r.Count == 5 {
			foundUpdatedAcc1 = true
		}
	}
	if !foundUpdatedAcc1 {
		t.Fatalf("incremental clear row missing after snapshot recovery: %+v", recRows)
	}

	// Pruning older than now-6h removes the 10h-old row while retaining newer rows.
	recoveredProj.pruneExpired(now.Add(-6 * time.Hour))
	_, _, remaining := recoveredProj.Coverage()
	if remaining != 7 { // 8 total (7 initial + 1 incremental) minus 1 row at -10h = 7
		t.Fatalf("expected 7 records after pruning >6h row, got %d", remaining)
	}

	// Corrupting snapshot bytes must be rejected by SHA-256 verification.
	rawSnap, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	rawSnap[len(rawSnap)-5] ^= 0xff
	corruptPath := filepath.Join(t.TempDir(), "corrupt.bin")
	if err := os.WriteFile(corruptPath, rawSnap, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoveredProj.LoadSnapshot(corruptPath); err == nil {
		t.Fatal("expected corrupted snapshot to be rejected")
	}
}
