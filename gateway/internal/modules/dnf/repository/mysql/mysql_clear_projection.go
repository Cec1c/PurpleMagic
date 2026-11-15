package mysql

import (
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const (
	clearSnapshotMagic      = "PMCLR001"
	defaultClearRetention   = 32 * 24 * time.Hour
	defaultClearSliceWindow = 4 * time.Hour
	defaultClearSlicePause  = 100 * time.Millisecond
	defaultClearTailOverlap = 5 * time.Second
	defaultClearTailMinGap  = 3 * time.Second
	defaultClearBgTick      = 15 * time.Second
	defaultClearSaveEvery   = 5 * time.Minute
)

// ClearProjectionConfig configures the gateway-side incremental clear_history projection.
type ClearProjectionConfig struct {
	SnapshotPath string
	Retention    time.Duration
	SliceWindow  time.Duration
	SlicePause   time.Duration
	TailOverlap  time.Duration
	TailMinGap   time.Duration
	BgInterval   time.Duration
	SaveInterval time.Duration
	Logger       func(string, ...any)
}

// clearRecord is pointer-free so Go's GC marks the backing slice noscan.
type clearRecord struct {
	finishedNano   int64
	dungeonID      int64
	durationMS     int64
	fatigue        int64
	accountIdx     uint32
	charIdx        uint32
	charNameIdx    uint32
	dungeonNameIdx uint32
	difficulty     int32
	shardIdx       uint16
	hasFatigue     bool
	dedupKey       [24]byte
}

type clearRecordView struct {
	AccountID     string
	CharacterID   string
	CharacterName string
	DungeonID     int64
	DungeonName   string
	Difficulty    int
	FinishedNano  int64
	DurationMS    int64
	Fatigue       int64
	HasFatigue    bool
}

type rawClearRow struct {
	shardIdx       uint16
	accountID      string
	characterID    string
	characterName  string
	dungeonID      int64
	dungeonName    string
	difficulty     int32
	finishedNano   int64
	durationMS     int64
	fatigue        int64
	hasFatigue     bool
	completionHash string
}

type clearShardCaps struct {
	probed            bool
	hasCompletionHash bool
	hasClearTimeIndex bool
}

type clearStringPool struct {
	toID map[string]uint32
	vals []string
}

func newClearStringPool() clearStringPool {
	return clearStringPool{
		toID: map[string]uint32{"": 0},
		vals: []string{""},
	}
}

func (p *clearStringPool) intern(s string) uint32 {
	if id, ok := p.toID[s]; ok {
		return id
	}
	// Detach substring backing arrays from transient SQL scan buffers.
	cloned := strings.Clone(s)
	id := uint32(len(p.vals))
	p.vals = append(p.vals, cloned)
	p.toID[cloned] = id
	return id
}

func (p *clearStringPool) get(id uint32) string {
	if int(id) < len(p.vals) {
		return p.vals[id]
	}
	return ""
}

// ClearHistoryProjection maintains a bounded, deduplicated in-memory projection
// of recent dnf_clear_history rows across all write shards, backed by an atomic
// local snapshot file and Newest-First bounded time-slice backfill.
type ClearHistoryProjection struct {
	router mysqlRouter
	cfg    ClearProjectionConfig

	mu           sync.RWMutex
	strings      clearStringPool
	records      []clearRecord
	seen         map[[24]byte]struct{}
	coveredFrom  time.Time
	watermark    time.Time
	lastTailSync time.Time
	lastSaved    time.Time
	dirty        bool

	shardMu   sync.Mutex
	shardCaps map[string]clearShardCaps

	tailMu      sync.Mutex
	tailLoading chan struct{}
	tailErr     error

	backfillMu sync.Mutex
	saveMu     sync.Mutex

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

// ClearProjectionFromGroup extracts the attached ClearHistoryProjection if enabled.
func ClearProjectionFromGroup(group repository.Group) *ClearHistoryProjection {
	if charStore, ok := group.Character.(*mysqlCharStore); ok {
		return charStore.clearProjection
	}
	return nil
}

func newClearHistoryProjection(router mysqlRouter, cfg ClearProjectionConfig) *ClearHistoryProjection {
	if cfg.Retention <= 0 {
		cfg.Retention = defaultClearRetention
	}
	if cfg.SliceWindow <= 0 {
		cfg.SliceWindow = defaultClearSliceWindow
	}
	if cfg.SlicePause < 0 {
		cfg.SlicePause = 0
	} else if cfg.SlicePause == 0 {
		cfg.SlicePause = defaultClearSlicePause
	}
	if cfg.TailOverlap <= 0 {
		cfg.TailOverlap = defaultClearTailOverlap
	}
	if cfg.TailMinGap <= 0 {
		cfg.TailMinGap = defaultClearTailMinGap
	}
	if cfg.BgInterval <= 0 {
		cfg.BgInterval = defaultClearBgTick
	}
	if cfg.SaveInterval <= 0 {
		cfg.SaveInterval = defaultClearSaveEvery
	}
	if cfg.Logger == nil {
		cfg.Logger = func(string, ...any) {}
	}
	return &ClearHistoryProjection{
		router:    router,
		cfg:       cfg,
		strings:   newClearStringPool(),
		seen:      make(map[[24]byte]struct{}),
		shardCaps: make(map[string]clearShardCaps),
		done:      make(chan struct{}),
	}
}

func (p *ClearHistoryProjection) nowUTC() time.Time {
	if p.router.now != nil {
		return p.router.now().UTC()
	}
	return time.Now().UTC()
}

func (p *ClearHistoryProjection) shardSignature() [32]byte {
	return sha256.Sum256([]byte(p.router.tablePrefix + "\x00" + strings.Join(p.router.writeDBs, "\x00")))
}

// Start loads any persisted local snapshot and launches the background
// Newest-First warmup and incremental watermark loop.
func (p *ClearHistoryProjection) Start(ctx context.Context) {
	workerCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	if p.cfg.SnapshotPath != "" {
		if err := p.LoadSnapshot(p.cfg.SnapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.cfg.Logger("clear-projection-snapshot-skip", "reason", "invalid_or_stale")
		}
	}
	go p.backgroundLoop(workerCtx)
}

// Close stops background workers and persists a final snapshot if dirty.
func (p *ClearHistoryProjection) Close() error {
	p.stopOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
			<-p.done
		} else {
			close(p.done)
		}
		if p.cfg.SnapshotPath != "" {
			p.mu.RLock()
			dirty := p.dirty && !p.coveredFrom.IsZero()
			p.mu.RUnlock()
			if dirty {
				_ = p.SaveSnapshot(p.cfg.SnapshotPath)
			}
		}
	})
	return nil
}

func (p *ClearHistoryProjection) backgroundLoop(ctx context.Context) {
	defer close(p.done)

	// Step 1: Catch up incremental tail (or initialize watermark on cold boot).
	_ = p.syncTail(ctx, true)

	// Step 2: Newest-First gentle 4-hour slice backfill down to now - Retention.
	targetFrom := p.nowUTC().Add(-p.cfg.Retention)
	if err := p.backfillDownTo(ctx, targetFrom, p.cfg.SlicePause); err == nil && p.cfg.SnapshotPath != "" {
		p.mu.RLock()
		dirty := p.dirty
		p.mu.RUnlock()
		if dirty {
			_ = p.SaveSnapshot(p.cfg.SnapshotPath)
		}
	}

	ticker := time.NewTicker(p.cfg.BgInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.syncTail(ctx, true)
			now := p.nowUTC()
			_ = p.backfillDownTo(ctx, now.Add(-p.cfg.Retention), p.cfg.SlicePause)
			p.pruneExpired(now.Add(-p.cfg.Retention))
			if p.cfg.SnapshotPath != "" {
				p.mu.RLock()
				shouldSave := p.dirty && (p.lastSaved.IsZero() || now.Sub(p.lastSaved) >= p.cfg.SaveInterval)
				p.mu.RUnlock()
				if shouldSave {
					_ = p.SaveSnapshot(p.cfg.SnapshotPath)
				}
			}
		}
	}
}

// Coverage returns the currently materialized [from, watermark] window.
func (p *ClearHistoryProjection) Coverage() (from, watermark time.Time, count int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.coveredFrom, p.watermark, len(p.records)
}

func computeClearDedupKey(r rawClearRow) [24]byte {
	var h [32]byte
	if r.completionHash != "" {
		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], r.shardIdx)
		sum := sha256.New()
		sum.Write([]byte("pk\x00"))
		sum.Write(buf[:])
		sum.Write([]byte(r.characterID))
		sum.Write([]byte{0})
		sum.Write([]byte(r.completionHash))
		copy(h[:], sum.Sum(nil))
	} else {
		var num [43]byte
		binary.LittleEndian.PutUint16(num[0:2], r.shardIdx)
		binary.LittleEndian.PutUint64(num[2:10], uint64(r.dungeonID))
		binary.LittleEndian.PutUint32(num[10:14], uint32(r.difficulty))
		binary.LittleEndian.PutUint64(num[14:22], uint64(r.finishedNano))
		binary.LittleEndian.PutUint64(num[22:30], uint64(r.durationMS))
		if r.hasFatigue {
			num[30] = 1
		}
		binary.LittleEndian.PutUint64(num[31:39], uint64(r.fatigue))
		sum := sha256.New()
		sum.Write([]byte("row\x00"))
		sum.Write(num[:39])
		sum.Write([]byte(r.accountID))
		sum.Write([]byte{0})
		sum.Write([]byte(r.characterID))
		copy(h[:], sum.Sum(nil))
	}
	var out [24]byte
	copy(out[:], h[:24])
	return out
}

func (p *ClearHistoryProjection) detectShardCaps(ctx context.Context, database string) clearShardCaps {
	p.shardMu.Lock()
	if caps, ok := p.shardCaps[database]; ok && caps.probed {
		p.shardMu.Unlock()
		return caps
	}
	p.shardMu.Unlock()

	table := mysqlTable(database, p.router.tablePrefix, "clear_history")
	var caps clearShardCaps

	// Probe completion_hash column using a lightweight metadata query on the table itself
	// (avoids slow multi-DB INFORMATION_SCHEMA scans on MariaDB).
	colRows, err := p.router.db.QueryContext(ctx, "SHOW COLUMNS FROM "+table+" LIKE 'completion_hash'")
	if err != nil {
		return caps
	}
	caps.probed = true
	if colRows.Next() {
		caps.hasCompletionHash = true
	}
	_ = colRows.Close()

	idxRows, err := p.router.db.QueryContext(ctx, "SHOW INDEX FROM "+table+" WHERE Key_name='clear_time'")
	if err == nil {
		if idxRows.Next() {
			caps.hasClearTimeIndex = true
		}
		_ = idxRows.Close()
	}

	p.shardMu.Lock()
	p.shardCaps[database] = caps
	p.shardMu.Unlock()
	return caps
}

func (p *ClearHistoryProjection) fetchWindowRows(ctx context.Context, from, to time.Time) ([]rawClearRow, error) {
	if len(p.router.writeDBs) == 0 {
		return nil, errors.New("clear history unavailable")
	}
	var out []rawClearRow
	for idx, database := range p.router.writeDBs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		caps := p.detectShardCaps(ctx, database)
		table := mysqlTable(database, p.router.tablePrefix, "clear_history")
		indexHint := ""
		if caps.hasClearTimeIndex {
			indexHint = " FORCE INDEX (clear_time)"
		}
		cols := "account_id,character_id,character_name,dungeon_id,dungeon_name,difficulty,finished_at,duration_ms,fatigue_spent"
		if caps.hasCompletionHash {
			cols += ",completion_hash"
		}
		query := "SELECT " + cols + " FROM " + table + indexHint + " WHERE finished_at>=? AND finished_at<?"
		rows, err := p.router.db.QueryContext(ctx, query, from.UTC(), to.UTC())
		if err != nil {
			// If clear_time index or completion_hash changed dynamically in a test, invalidate probe cache.
			p.shardMu.Lock()
			delete(p.shardCaps, database)
			p.shardMu.Unlock()
			return nil, err
		}
		for rows.Next() {
			var r rawClearRow
			r.shardIdx = uint16(idx)
			var finished time.Time
			var fatigue sql.NullInt64
			var scanErr error
			if caps.hasCompletionHash {
				scanErr = rows.Scan(&r.accountID, &r.characterID, &r.characterName, &r.dungeonID, &r.dungeonName, &r.difficulty, &finished, &r.durationMS, &fatigue, &r.completionHash)
			} else {
				scanErr = rows.Scan(&r.accountID, &r.characterID, &r.characterName, &r.dungeonID, &r.dungeonName, &r.difficulty, &finished, &r.durationMS, &fatigue)
			}
			if scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			r.finishedNano = finished.UTC().UnixNano()
			if fatigue.Valid {
				r.hasFatigue = true
				r.fatigue = fatigue.Int64
			}
			out = append(out, r)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *ClearHistoryProjection) ingestRowsLocked(rows []rawClearRow) int {
	added := 0
	for _, r := range rows {
		key := computeClearDedupKey(r)
		if _, exists := p.seen[key]; exists {
			continue
		}
		p.seen[key] = struct{}{}
		p.records = append(p.records, clearRecord{
			finishedNano:   r.finishedNano,
			dungeonID:      r.dungeonID,
			durationMS:     r.durationMS,
			fatigue:        r.fatigue,
			accountIdx:     p.strings.intern(r.accountID),
			charIdx:        p.strings.intern(r.characterID),
			charNameIdx:    p.strings.intern(r.characterName),
			dungeonNameIdx: p.strings.intern(r.dungeonName),
			difficulty:     r.difficulty,
			shardIdx:       r.shardIdx,
			hasFatigue:     r.hasFatigue,
			dedupKey:       key,
		})
		added++
	}
	if added > 0 {
		p.dirty = true
	}
	return added
}

// syncTail advances watermark to now using singleflight coalescing across concurrent callers.
func (p *ClearHistoryProjection) syncTail(ctx context.Context, force bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := p.nowUTC()
	if !force {
		p.mu.RLock()
		fresh := !p.watermark.IsZero() && !p.lastTailSync.IsZero() && now.Sub(p.lastTailSync) < p.cfg.TailMinGap
		p.mu.RUnlock()
		if fresh {
			return nil
		}
	}

	for {
		p.tailMu.Lock()
		if p.tailLoading != nil {
			wait := p.tailLoading
			p.tailMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wait:
				p.tailMu.Lock()
				err := p.tailErr
				p.tailMu.Unlock()
				return err
			}
		}
		done := make(chan struct{})
		p.tailLoading = done
		p.tailMu.Unlock()

		err := p.doSyncTail(ctx)

		p.tailMu.Lock()
		p.tailErr = err
		p.tailLoading = nil
		close(done)
		p.tailMu.Unlock()
		return err
	}
}

func (p *ClearHistoryProjection) doSyncTail(ctx context.Context) error {
	now := p.nowUTC()
	upper := now.Add(time.Second)

	p.mu.RLock()
	wm := p.watermark
	covFrom := p.coveredFrom
	p.mu.RUnlock()

	if wm.IsZero() || covFrom.IsZero() {
		// Cold start: initialize empty anchor at `now` and pull the most recent SliceWindow immediately
		// so recent queries have immediate data, then Newest-First backfill walks backwards.
		firstFrom := now.Add(-p.cfg.SliceWindow)
		rows, err := p.fetchWindowRows(ctx, firstFrom, upper)
		if err != nil {
			return err
		}
		p.mu.Lock()
		if p.watermark.IsZero() || p.coveredFrom.IsZero() {
			p.ingestRowsLocked(rows)
			p.coveredFrom = firstFrom
			p.watermark = now
			p.lastTailSync = now
			p.dirty = true
		}
		p.mu.Unlock()
		return nil
	}

	tailFrom := wm.Add(-p.cfg.TailOverlap)
	if tailFrom.Before(covFrom) {
		tailFrom = covFrom
	}
	rows, err := p.fetchWindowRows(ctx, tailFrom, upper)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.ingestRowsLocked(rows)
	if now.After(p.watermark) {
		p.watermark = now
	}
	p.lastTailSync = now
	p.mu.Unlock()
	return nil
}

// backfillDownTo walks backwards (Newest -> Oldest) from coveredFrom down to targetFrom
// in bounded SliceWindow chunks so cold InnoDB pages are never flooded all at once.
func (p *ClearHistoryProjection) backfillDownTo(ctx context.Context, targetFrom time.Time, pause time.Duration) error {
	p.backfillMu.Lock()
	defer p.backfillMu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.RLock()
		covFrom := p.coveredFrom
		wm := p.watermark
		p.mu.RUnlock()

		if wm.IsZero() || covFrom.IsZero() {
			if err := p.syncTail(ctx, true); err != nil {
				return err
			}
			continue
		}
		if !covFrom.After(targetFrom) {
			return nil
		}

		lo := covFrom.Add(-p.cfg.SliceWindow)
		if lo.Before(targetFrom) {
			lo = targetFrom
		}
		// Slight 1s overlap at slice seam; deduplicated via seen[dedupKey].
		rows, err := p.fetchWindowRows(ctx, lo, covFrom.Add(time.Second))
		if err != nil {
			return err
		}
		p.mu.Lock()
		p.ingestRowsLocked(rows)
		if lo.Before(p.coveredFrom) {
			p.coveredFrom = lo
			p.dirty = true
		}
		p.mu.Unlock()

		if !lo.After(targetFrom) {
			return nil
		}
		if pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}

func (p *ClearHistoryProjection) pruneExpired(cutoff time.Time) {
	cutoffNano := cutoff.UTC().UnixNano()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.coveredFrom.IsZero() || !p.coveredFrom.Before(cutoff) {
		return
	}
	expired := 0
	for i := range p.records {
		if p.records[i].finishedNano < cutoffNano {
			expired++
		}
	}
	p.coveredFrom = cutoff
	if expired == 0 {
		return
	}
	kept := make([]clearRecord, 0, len(p.records)-expired)
	newSeen := make(map[[24]byte]struct{}, len(p.records)-expired)
	newStrings := newClearStringPool()
	for _, rec := range p.records {
		if rec.finishedNano < cutoffNano {
			continue
		}
		rec.accountIdx = newStrings.intern(p.strings.get(rec.accountIdx))
		rec.charIdx = newStrings.intern(p.strings.get(rec.charIdx))
		rec.charNameIdx = newStrings.intern(p.strings.get(rec.charNameIdx))
		rec.dungeonNameIdx = newStrings.intern(p.strings.get(rec.dungeonNameIdx))
		kept = append(kept, rec)
		newSeen[rec.dedupKey] = struct{}{}
	}
	p.records = kept
	p.seen = newSeen
	p.strings = newStrings
	p.dirty = true
}

// ensureRange ensures the projection covers [from, to) if `from` falls within
// the retention window. Returns (true, nil) when memory can serve [from, to),
// (false, nil) when `from` is older than retention (caller falls back to SQL),
// or (_, err) on upstream/cancellation error.
func (p *ClearHistoryProjection) ensureRange(ctx context.Context, from, to time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := p.nowUTC()
	oldestRetained := now.Add(-p.cfg.Retention)
	if from.Before(oldestRetained) {
		return false, nil
	}

	p.mu.RLock()
	needTail := p.watermark.IsZero() || to.After(p.watermark) || now.Sub(p.lastTailSync) >= p.cfg.TailMinGap
	p.mu.RUnlock()
	if needTail {
		if err := p.syncTail(ctx, to.After(now.Add(-p.cfg.TailMinGap))); err != nil {
			return false, err
		}
	}

	p.mu.RLock()
	covered := !p.coveredFrom.IsZero() && !p.coveredFrom.After(from)
	p.mu.RUnlock()
	if covered {
		return true, nil
	}

	// Cold-start backfill has not reached `from` yet: expedite bounded 4-hour slices
	// without inter-slice sleep up to caller ctx deadline.
	if err := p.backfillDownTo(ctx, from, 0); err != nil {
		return false, err
	}
	return true, nil
}

// SaveSnapshot writes an atomic binary checkpoint (`path + ".tmp"` -> fsync -> rename).
func (p *ClearHistoryProjection) SaveSnapshot(path string) error {
	if path == "" {
		return nil
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()

	p.mu.RLock()
	if p.coveredFrom.IsZero() || p.watermark.IsZero() {
		p.mu.RUnlock()
		return nil
	}
	covNano := p.coveredFrom.UTC().UnixNano()
	wmNano := p.watermark.UTC().UnixNano()
	strs := make([]string, len(p.strings.vals))
	copy(strs, p.strings.vals)
	recs := make([]clearRecord, len(p.records))
	copy(recs, p.records)
	p.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cleanup := func(e error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return e
	}

	hasher := sha256.New()
	bw := bufio.NewWriterSize(io.MultiWriter(f, hasher), 256<<10)

	var header [64]byte
	copy(header[0:8], clearSnapshotMagic)
	sig := p.shardSignature()
	copy(header[8:40], sig[:])
	binary.LittleEndian.PutUint64(header[40:48], uint64(covNano))
	binary.LittleEndian.PutUint64(header[48:56], uint64(wmNano))
	binary.LittleEndian.PutUint32(header[56:60], uint32(len(strs)))
	binary.LittleEndian.PutUint32(header[60:64], uint32(len(recs)))
	if _, err = bw.Write(header[:]); err != nil {
		return cleanup(err)
	}

	var lenBuf [4]byte
	for _, s := range strs {
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(s)))
		if _, err = bw.Write(lenBuf[:]); err != nil {
			return cleanup(err)
		}
		if len(s) > 0 {
			if _, err = bw.WriteString(s); err != nil {
				return cleanup(err)
			}
		}
	}

	var recBuf [79]byte
	for _, r := range recs {
		binary.LittleEndian.PutUint64(recBuf[0:8], uint64(r.finishedNano))
		binary.LittleEndian.PutUint64(recBuf[8:16], uint64(r.dungeonID))
		binary.LittleEndian.PutUint64(recBuf[16:24], uint64(r.durationMS))
		binary.LittleEndian.PutUint64(recBuf[24:32], uint64(r.fatigue))
		binary.LittleEndian.PutUint32(recBuf[32:36], r.accountIdx)
		binary.LittleEndian.PutUint32(recBuf[36:40], r.charIdx)
		binary.LittleEndian.PutUint32(recBuf[40:44], r.charNameIdx)
		binary.LittleEndian.PutUint32(recBuf[44:48], r.dungeonNameIdx)
		binary.LittleEndian.PutUint32(recBuf[48:52], uint32(r.difficulty))
		binary.LittleEndian.PutUint16(recBuf[52:54], r.shardIdx)
		if r.hasFatigue {
			recBuf[54] = 1
		} else {
			recBuf[54] = 0
		}
		copy(recBuf[55:79], r.dedupKey[:])
		if _, err = bw.Write(recBuf[:]); err != nil {
			return cleanup(err)
		}
	}
	if err = bw.Flush(); err != nil {
		return cleanup(err)
	}
	digest := hasher.Sum(nil)
	if _, err = f.Write(digest); err != nil {
		return cleanup(err)
	}
	if err = f.Sync(); err != nil {
		return cleanup(err)
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	now := p.nowUTC()
	p.mu.Lock()
	p.dirty = false
	p.lastSaved = now
	p.mu.Unlock()
	return nil
}

// LoadSnapshot restores persisted projection state after verifying shard identity and SHA-256 integrity.
func (p *ClearHistoryProjection) LoadSnapshot(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 64+32 {
		return errors.New("clear snapshot truncated")
	}
	payloadBytes := info.Size() - 32
	hasher := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(io.LimitReader(f, payloadBytes), hasher), 256<<10)

	var header [64]byte
	if _, err = io.ReadFull(br, header[:]); err != nil {
		return err
	}
	sig := p.shardSignature()
	if string(header[0:8]) != clearSnapshotMagic || string(header[8:40]) != string(sig[:]) {
		return errors.New("clear snapshot header or shard mismatch")
	}
	covNano := int64(binary.LittleEndian.Uint64(header[40:48]))
	wmNano := int64(binary.LittleEndian.Uint64(header[48:56]))
	numStrings := binary.LittleEndian.Uint32(header[56:60])
	numRecords := binary.LittleEndian.Uint32(header[60:64])
	if numStrings == 0 || numStrings > 5_000_000 || numRecords > 20_000_000 {
		return errors.New("clear snapshot bounds exceeded")
	}

	pool := clearStringPool{
		toID: make(map[string]uint32, int(numStrings)),
		vals: make([]string, numStrings),
	}
	var lenBuf [4]byte
	for i := uint32(0); i < numStrings; i++ {
		if _, err = io.ReadFull(br, lenBuf[:]); err != nil {
			return err
		}
		slen := binary.LittleEndian.Uint32(lenBuf[:])
		if slen > 4096 {
			return errors.New("clear snapshot string too large")
		}
		if slen == 0 {
			pool.vals[i] = ""
			pool.toID[""] = i
			continue
		}
		buf := make([]byte, slen)
		if _, err = io.ReadFull(br, buf); err != nil {
			return err
		}
		s := string(buf)
		pool.vals[i] = s
		pool.toID[s] = i
	}

	cutoffNano := p.nowUTC().Add(-p.cfg.Retention).UnixNano()
	recs := make([]clearRecord, 0, int(numRecords))
	seen := make(map[[24]byte]struct{}, int(numRecords))
	var recBuf [79]byte
	for i := uint32(0); i < numRecords; i++ {
		if _, err = io.ReadFull(br, recBuf[:]); err != nil {
			return err
		}
		r := clearRecord{
			finishedNano:   int64(binary.LittleEndian.Uint64(recBuf[0:8])),
			dungeonID:      int64(binary.LittleEndian.Uint64(recBuf[8:16])),
			durationMS:     int64(binary.LittleEndian.Uint64(recBuf[16:24])),
			fatigue:        int64(binary.LittleEndian.Uint64(recBuf[24:32])),
			accountIdx:     binary.LittleEndian.Uint32(recBuf[32:36]),
			charIdx:        binary.LittleEndian.Uint32(recBuf[36:40]),
			charNameIdx:    binary.LittleEndian.Uint32(recBuf[40:44]),
			dungeonNameIdx: binary.LittleEndian.Uint32(recBuf[44:48]),
			difficulty:     int32(binary.LittleEndian.Uint32(recBuf[48:52])),
			shardIdx:       binary.LittleEndian.Uint16(recBuf[52:54]),
			hasFatigue:     recBuf[54] != 0,
		}
		copy(r.dedupKey[:], recBuf[55:79])
		if r.accountIdx >= numStrings || r.charIdx >= numStrings || r.charNameIdx >= numStrings || r.dungeonNameIdx >= numStrings {
			return errors.New("clear snapshot string index out of range")
		}
		if r.finishedNano < cutoffNano {
			continue
		}
		if _, dup := seen[r.dedupKey]; dup {
			continue
		}
		seen[r.dedupKey] = struct{}{}
		recs = append(recs, r)
	}

	// Drain any remaining bytes up to payloadBytes (should be 0) and verify trailer SHA-256.
	if _, err = io.Copy(io.Discard, br); err != nil {
		return err
	}
	var expectedDigest [32]byte
	if _, err = f.ReadAt(expectedDigest[:], payloadBytes); err != nil {
		return err
	}
	actualDigest := hasher.Sum(nil)
	if string(actualDigest) != string(expectedDigest[:]) {
		return errors.New("clear snapshot checksum mismatch")
	}

	covTime := time.Unix(0, covNano).UTC()
	cutoffTime := time.Unix(0, cutoffNano).UTC()
	if covTime.Before(cutoffTime) {
		covTime = cutoffTime
	}
	wmTime := time.Unix(0, wmNano).UTC()
	if covTime.After(wmTime) {
		return errors.New("clear snapshot inverted coverage")
	}

	p.mu.Lock()
	p.strings = pool
	p.records = recs
	p.seen = seen
	p.coveredFrom = covTime
	p.watermark = wmTime
	p.dirty = false
	p.lastSaved = p.nowUTC()
	p.mu.Unlock()
	return nil
}

func utcHourBucket(unixNano int64) int64 {
	sec := unixNano / 1_000_000_000
	if unixNano < 0 && unixNano%1_000_000_000 != 0 {
		sec--
	}
	hour := sec / 3600
	if sec < 0 && sec%3600 != 0 {
		hour--
	}
	return hour
}

func containsFoldSubstring(haystack, needleLower string) bool {
	if needleLower == "" {
		return true
	}
	if haystack == "" {
		return false
	}
	return strings.Contains(strings.ToLower(haystack), needleLower)
}

func (p *ClearHistoryProjection) scanRange(ctx context.Context, from, to time.Time, visit func(clearRecordView)) (bool, error) {
	ok, err := p.ensureRange(ctx, from, to)
	if err != nil || !ok {
		return false, err
	}
	fromNano := from.UTC().UnixNano()
	toNano := to.UTC().UnixNano()

	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, r := range p.records {
		if r.finishedNano < fromNano || r.finishedNano >= toNano {
			continue
		}
		visit(clearRecordView{
			AccountID:     p.strings.get(r.accountIdx),
			CharacterID:   p.strings.get(r.charIdx),
			CharacterName: p.strings.get(r.charNameIdx),
			DungeonID:     r.dungeonID,
			DungeonName:   p.strings.get(r.dungeonNameIdx),
			Difficulty:    int(r.difficulty),
			FinishedNano:  r.finishedNano,
			DurationMS:    r.durationMS,
			Fatigue:       r.fatigue,
			HasFatigue:    r.hasFatigue,
		})
	}
	return true, nil
}

func (p *ClearHistoryProjection) dashboardClearCounts(ctx context.Context, from, to time.Time, threshold int64) (clears, fatigue, samples, fast int64, used bool, err error) {
	used, err = p.scanRange(ctx, from, to, func(r clearRecordView) {
		clears++
		if r.HasFatigue {
			samples++
			fatigue += r.Fatigue
		}
		if r.DurationMS > 0 && r.DurationMS < threshold {
			fast++
		}
	})
	return
}

func (p *ClearHistoryProjection) queryAccountClears(ctx context.Context, q moderation.Query) ([]moderation.AccountClearStats, bool, error) {
	excluded := make(map[string]struct{}, len(q.ExcludedAccountIDs))
	for _, id := range q.ExcludedAccountIDs {
		excluded[id] = struct{}{}
	}

	type accountAgg struct {
		count        int64
		fastest      int64
		durationSum  int64
		timedRuns    int64
		lastNano     int64
		fatigueSum   int64
		sampled      int64
		rateFatigue  int64
		rateDuration int64
		hours        map[int64]int64
	}
	aggs := make(map[string]*accountAgg)

	used, err := p.scanRange(ctx, q.From, q.To, func(r clearRecordView) {
		if q.AccountID != "" && r.AccountID != q.AccountID {
			return
		}
		if q.DungeonID != 0 && r.DungeonID != q.DungeonID {
			return
		}
		if _, banned := excluded[r.AccountID]; banned {
			return
		}
		a := aggs[r.AccountID]
		if a == nil {
			a = &accountAgg{hours: make(map[int64]int64, 4)}
			aggs[r.AccountID] = a
		}
		a.count++
		if a.count == 1 || r.FinishedNano > a.lastNano {
			a.lastNano = r.FinishedNano
		}
		a.hours[utcHourBucket(r.FinishedNano)]++
		if r.DurationMS > 0 {
			a.timedRuns++
			a.durationSum += r.DurationMS
			if a.fastest == 0 || r.DurationMS < a.fastest {
				a.fastest = r.DurationMS
			}
		}
		if r.HasFatigue {
			a.sampled++
			a.fatigueSum += r.Fatigue
			if r.DurationMS > 0 {
				a.rateFatigue += r.Fatigue
				a.rateDuration += r.DurationMS
			}
		}
	})
	if err != nil || !used {
		return nil, false, err
	}

	out := make([]moderation.AccountClearStats, 0, len(aggs))
	for accountID, a := range aggs {
		var peak int64
		for _, runs := range a.hours {
			if runs > peak {
				peak = runs
			}
		}
		if q.MaxDurationMS > 0 && (a.timedRuns == 0 || a.fastest > q.MaxDurationMS) {
			continue
		}
		if q.MinHourlyPeak > 0 && peak < q.MinHourlyPeak {
			continue
		}
		row := moderation.AccountClearStats{
			AccountID:      accountID,
			Count:          a.count,
			FastestMS:      a.fastest,
			FatigueSamples: a.sampled,
			PeakHourly:     peak,
			LastClearAt:    time.Unix(0, a.lastNano).UTC(),
		}
		if a.timedRuns > 0 {
			row.AverageMS = int64(math.Round(float64(a.durationSum) / float64(a.timedRuns)))
		}
		if a.sampled > 0 {
			spent := a.fatigueSum
			row.FatigueSpent = &spent
		}
		if a.rateDuration > 0 {
			fps := float64(a.rateFatigue) * 1000.0 / float64(a.rateDuration)
			row.FatiguePerSecond = &fps
		}
		out = append(out, row)
	}
	return out, true, nil
}

func (p *ClearHistoryProjection) queryClears(ctx context.Context, q moderation.Query) (moderation.ClearPage, bool, error) {
	out := moderation.ClearPage{Rows: []moderation.ClearStats{}, From: q.From, To: q.To}

	excluded := make(map[string]struct{}, len(q.ExcludedAccountIDs))
	for _, id := range q.ExcludedAccountIDs {
		excluded[id] = struct{}{}
	}
	searchLower := strings.ToLower(q.Search)

	type groupKey struct {
		accountID   string
		characterID string
		dungeonID   int64
		difficulty  int
	}
	type clearAgg struct {
		key         groupKey
		charName    string
		dungeonName string
		count       int64
		lastNano    int64
		fastest     int64
		durationSum int64
		timedRuns   int64
		fatigueSum  int64
		sampled     int64
		hours       map[int64]int64
	}
	// Notice SQL line 57 partitions hourly peak by (character_id, dungeon_id, difficulty, hour) across source_clears!
	type charHourKey struct {
		characterID string
		dungeonID   int64
		difficulty  int
		hour        int64
	}
	charHours := make(map[charHourKey]int64)
	groups := make(map[groupKey]*clearAgg)

	used, err := p.scanRange(ctx, q.From, q.To, func(r clearRecordView) {
		if q.AccountID != "" && r.AccountID != q.AccountID {
			return
		}
		if q.DungeonID != 0 && r.DungeonID != q.DungeonID {
			return
		}
		if _, banned := excluded[r.AccountID]; banned {
			return
		}
		if q.Search != "" {
			matched := r.CharacterID == q.Search ||
				r.AccountID == q.Search ||
				containsFoldSubstring(r.CharacterName, searchLower) ||
				containsFoldSubstring(r.DungeonName, searchLower)
			if !matched && len(q.AccountNames) > 0 {
				if acctName := q.AccountNames[r.AccountID]; acctName != "" && containsFoldSubstring(acctName, searchLower) {
					matched = true
				}
			}
			if !matched {
				return
			}
		}

		hb := utcHourBucket(r.FinishedNano)
		chk := charHourKey{r.CharacterID, r.DungeonID, r.Difficulty, hb}
		charHours[chk]++

		gk := groupKey{r.AccountID, r.CharacterID, r.DungeonID, r.Difficulty}
		g := groups[gk]
		if g == nil {
			g = &clearAgg{
				key:         gk,
				charName:    r.CharacterName,
				dungeonName: r.DungeonName,
				hours:       make(map[int64]int64, 4),
			}
			groups[gk] = g
		} else {
			if r.CharacterName > g.charName {
				g.charName = r.CharacterName
			}
			if r.DungeonName > g.dungeonName {
				g.dungeonName = r.DungeonName
			}
		}
		g.count++
		if g.count == 1 || r.FinishedNano > g.lastNano {
			g.lastNano = r.FinishedNano
		}
		g.hours[hb] = 1
		if r.DurationMS > 0 {
			g.timedRuns++
			g.durationSum += r.DurationMS
			if g.fastest == 0 || r.DurationMS < g.fastest {
				g.fastest = r.DurationMS
			}
		}
		if r.HasFatigue {
			g.sampled++
			g.fatigueSum += r.Fatigue
		}
	})
	if err != nil || !used {
		return out, false, err
	}

	type sortableClear struct {
		row      moderation.ClearStats
		hasTimed bool
		rawAvgMS float64
	}
	items := make([]sortableClear, 0, len(groups))
	for _, g := range groups {
		var peak int64
		for hb := range g.hours {
			if runs := charHours[charHourKey{g.key.characterID, g.key.dungeonID, g.key.difficulty, hb}]; runs > peak {
				peak = runs
			}
		}
		if q.MaxDurationMS > 0 && (g.timedRuns == 0 || g.fastest > q.MaxDurationMS) {
			continue
		}
		if q.MinHourlyPeak > 0 && peak < q.MinHourlyPeak {
			continue
		}
		sc := sortableClear{
			row: moderation.ClearStats{
				AccountID:      g.key.accountID,
				CharacterID:    g.key.characterID,
				CharacterName:  g.charName,
				DungeonID:      g.key.dungeonID,
				DungeonName:    g.dungeonName,
				Difficulty:     g.key.difficulty,
				Count:          g.count,
				PeakHourly:     peak,
				LastClearAt:    time.Unix(0, g.lastNano).UTC(),
				FastestMS:      g.fastest,
				FatigueSamples: g.sampled,
			},
			hasTimed: g.timedRuns > 0,
		}
		if g.timedRuns > 0 {
			sc.rawAvgMS = float64(g.durationSum) / float64(g.timedRuns)
			sc.row.AverageMS = int64(math.Round(sc.rawAvgMS))
		}
		if g.sampled > 0 {
			spent := g.fatigueSum
			avgFat := float64(g.fatigueSum) / float64(g.sampled)
			sc.row.FatigueSpent = &spent
			sc.row.AverageFatigue = &avgFat
		}
		items = append(items, sc)
	}

	slices.SortFunc(items, func(a, b sortableClear) int {
		var res int
		var missingA, missingB bool
		switch q.SortBy {
		case "accountId":
			res = cmp.Compare(a.row.AccountID, b.row.AccountID)
		case "characterId":
			res = cmp.Compare(a.row.CharacterID, b.row.CharacterID)
		case "characterName":
			res = cmp.Compare(a.row.CharacterName, b.row.CharacterName)
		case "dungeonId":
			res = cmp.Compare(a.row.DungeonID, b.row.DungeonID)
		case "dungeonName":
			res = cmp.Compare(a.row.DungeonName, b.row.DungeonName)
		case "difficulty":
			res = cmp.Compare(a.row.Difficulty, b.row.Difficulty)
		case "count":
			res = cmp.Compare(a.row.Count, b.row.Count)
		case "fastestMs":
			missingA, missingB = !a.hasTimed, !b.hasTimed
			res = cmp.Compare(a.row.FastestMS, b.row.FastestMS)
		case "averageMs":
			missingA, missingB = !a.hasTimed, !b.hasTimed
			res = cmp.Compare(a.rawAvgMS, b.rawAvgMS)
		case "peakHourly":
			res = cmp.Compare(a.row.PeakHourly, b.row.PeakHourly)
		case "lastClearAt":
			res = a.row.LastClearAt.Compare(b.row.LastClearAt)
		case "fatigueSpent":
			missingA, missingB = a.row.FatigueSpent == nil, b.row.FatigueSpent == nil
			if !missingA && !missingB {
				res = cmp.Compare(*a.row.FatigueSpent, *b.row.FatigueSpent)
			}
		case "averageFatigue":
			missingA, missingB = a.row.AverageFatigue == nil, b.row.AverageFatigue == nil
			if !missingA && !missingB {
				res = cmp.Compare(*a.row.AverageFatigue, *b.row.AverageFatigue)
			}
		}
		if missingA != missingB {
			if missingA {
				return 1
			}
			return -1
		}
		if q.SortOrder == "desc" {
			res = -res
		}
		if res != 0 {
			return res
		}
		if c := cmp.Compare(a.row.CharacterID, b.row.CharacterID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.row.DungeonID, b.row.DungeonID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.row.Difficulty, b.row.Difficulty); c != 0 {
			return c
		}
		return cmp.Compare(a.row.AccountID, b.row.AccountID)
	})

	if q.Offset >= len(items) {
		return out, true, nil
	}
	sliced := items[q.Offset:]
	if len(sliced) > q.Limit {
		out.HasMore = true
		sliced = sliced[:q.Limit]
	}
	out.Rows = make([]moderation.ClearStats, len(sliced))
	for i := range sliced {
		out.Rows[i] = sliced[i].row
	}
	return out, true, nil
}
