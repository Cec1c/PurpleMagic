package gmtool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/premium"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/raid"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	platformpvf "dnf90.local/gateway/internal/platform/pvf"
	"dnf90.local/gateway/internal/servicehttp"
)

func dungeonQuotaFixtureSource() *itemCacheTestSource {
	raidDoc := func(weekly int) string {
		return fmt.Sprintf("[PHASE MAX]\n1\n[START DELAY TIME]\n60\n[WEEKLY CLEAR COUNT]\n%d\n[PHASE TIME OVER]\n1800\n[PHASE]\n[DUNGEON INFO]\n[INDEX]\n1001\n[INIT STATE]\n`open`\n[/DUNGEON INFO]\n[/PHASE]\n", weekly)
	}
	return newItemCacheTestSource(readonlyTestSource{
		"etc/dungeoncyclesystem.etc": "[dungeon cycle]\n45 0 3570 3 0\n45 0 3571 1 1\n45 1 5097 3 0\n45 1 5098 1 1\n99 0 9001 5 0\n[/dungeon cycle]\n",
		"etc/raid/anton.etc":         raidDoc(2),
		"etc/raid/luke/luke.etc":     raidDoc(2),
		"etc/raid/fiendwar/fiendwar.etc": "[WEEKLY CLEAR COUNT]\n2\n[/WEEKLY CLEAR COUNT]\n[PHASE]\n[DUNGEON INFO]\n[INDEX]\n2001\n[/DUNGEON INFO]\n[/PHASE]\n",
	})
}

type memoryQuotaSettings struct {
	mu       sync.Mutex
	settings map[string]repository.SettingsRecord
}

func (m *memoryQuotaSettings) Load(_ context.Context, scope string) (repository.SettingsRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.settings[scope]
	return v, ok, nil
}

func (m *memoryQuotaSettings) Save(_ context.Context, record repository.SettingsRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := repository.SettingsRecord{Scope: record.Scope, Values: map[string]string{}, UpdatedAt: record.UpdatedAt}
	for k, v := range record.Values {
		cp.Values[k] = v
	}
	m.settings[record.Scope] = cp
	return nil
}

type memoryQuotaStore struct {
	repository.CharacterRepository
	repository.CharacterResourceReader
	mu       sync.Mutex
	chars    map[string]repository.CharacterRecord
	receipts map[string][]byte
	log      []repository.ManagementLogRow
	seq      int64
}

func newMemoryQuotaStore() *memoryQuotaStore {
	return &memoryQuotaStore{
		chars:    map[string]repository.CharacterRecord{},
		receipts: map[string][]byte{},
	}
}

func (m *memoryQuotaStore) SummariesForAccounts(_ context.Context, accountIDs []string) ([]repository.CharacterSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.CharacterSummary
	for _, c := range m.chars {
		if slices.Contains(accountIDs, c.AccountID) && c.Stats["delete_flag"] == 0 {
			out = append(out, repository.CharacterSummary{CharacterID: c.CharacterID, AccountID: c.AccountID, Name: c.Name, Job: c.Job, Level: c.Level, UpdatedAt: c.UpdatedAt})
		}
	}
	return out, nil
}

func (m *memoryQuotaStore) FindSummary(_ context.Context, id string) (repository.CharacterSummary, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.chars[id]
	if !ok || c.Stats["delete_flag"] != 0 {
		return repository.CharacterSummary{}, false, nil
	}
	return repository.CharacterSummary{CharacterID: c.CharacterID, AccountID: c.AccountID, Name: c.Name, Job: c.Job, Level: c.Level, UpdatedAt: c.UpdatedAt}, true, nil
}

func (m *memoryQuotaStore) SearchSummaries(_ context.Context, _, _ string, _ int) ([]repository.CharacterSummary, error) {
	return nil, nil
}

func (m *memoryQuotaStore) LoadDungeonQuotaManagement(_ context.Context, id string) (repository.DungeonQuotaManagementState, error) {
	c, ok := m.chars[id]
	if !ok {
		return repository.DungeonQuotaManagementState{}, os.ErrNotExist
	}
	return repository.DungeonQuotaManagementState{Character: repository.CloneCharacter(c)}, nil
}

func (m *memoryQuotaStore) ResetDungeonQuotaKeys(_ context.Context, id string, deleteKeys []string, sequence int64, updatedAt time.Time) error {
	c, ok := m.chars[id]
	if !ok {
		return os.ErrNotExist
	}
	next := repository.CloneCharacter(c)
	for _, k := range deleteKeys {
		delete(next.Stats, k)
	}
	next.Stats["gm_dungeon_quota_sequence_v1"] = sequence
	next.UpdatedAt = updatedAt
	m.chars[id] = next
	return nil
}

type memoryQuotaReceipts struct {
	store *memoryQuotaStore
	char  string
}

func (r memoryQuotaReceipts) Load(_ context.Context, key string) ([]byte, bool, error) {
	raw, ok := r.store.receipts[key]
	return slices.Clone(raw), ok, nil
}

func (r memoryQuotaReceipts) Append(_ context.Context, key string, payload []byte) error {
	if _, exists := r.store.receipts[key]; exists {
		return adminops.ErrIdempotency
	}
	cp := slices.Clone(payload)
	r.store.receipts[key] = cp
	r.store.seq++
	r.store.log = append([]repository.ManagementLogRow{{Sequence: r.store.seq, Payload: cp}}, r.store.log...)
	return nil
}

func (m *memoryQuotaStore) WithinDungeonQuotaManagement(_ context.Context, _, characterID string, fn func(repository.DungeonQuotaManagementRepository, repository.ManagementReceiptStore) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapChars := map[string]repository.CharacterRecord{}
	for k, v := range m.chars {
		snapChars[k] = repository.CloneCharacter(v)
	}
	snapReceipts := map[string][]byte{}
	for k, v := range m.receipts {
		snapReceipts[k] = slices.Clone(v)
	}
	snapLog := slices.Clone(m.log)
	snapSeq := m.seq
	if err := fn(m, memoryQuotaReceipts{store: m, char: characterID}); err != nil {
		m.chars = snapChars
		m.receipts = snapReceipts
		m.log = snapLog
		m.seq = snapSeq
		return err
	}
	return nil
}

func (m *memoryQuotaStore) DungeonQuotaManagementLog(_ context.Context, _ string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.ManagementLogRow
	for _, row := range m.log {
		if before == 0 || row.Sequence < before {
			out = append(out, row)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type stubSkillRepo struct{ repository.SkillRepository }
type stubInvRepo struct{ repository.InventoryRepository }
type stubMailRepo struct{ repository.MailboxRepository }

func TestV1DungeonQuotasInMemoryAndExternal(t *testing.T) {
	fixedNow := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) // 20:00 Beijing time
	serviceDay := premium.ServiceDayID(fixedNow)
	policy, err := adminops.DefaultDungeonQuotaWeeklyPolicy()
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := raid.WeeklyEpoch(fixedNow, policy)
	if err != nil {
		t.Fatal(err)
	}

	store := newMemoryQuotaStore()
	store.chars["10"] = repository.CharacterRecord{
		CharacterID: "10",
		AccountID:   "dnf:1001",
		Name:        "魔皇测试",
		Job:         "14",
		Level:       90,
		UpdatedAt:   fixedNow.Add(-time.Hour),
		Stats: map[string]int64{
			"gold": 123456,
			// Pandemonium Rift: 2/3 used today
			"dungeon_daily_entry_316_day":      serviceDay,
			"dungeon_daily_entry_316_used":     2,
			"dungeon_daily_entry_316_nonce_hi": 11,
			"dungeon_daily_entry_316_nonce_lo": 22,
			// Echon Temple: 3570 cleared twice (daily=2, cycle=2), 5097 entered once without clear (cycle=1) => entries=3, clears=2
			"dungeon_daily_entry_3570_day":  serviceDay,
			"dungeon_daily_entry_3570_used": 2,
			"dungeon_cycle_entry_3570_day":  serviceDay,
			"dungeon_cycle_entry_3570_used": 2,
			"dungeon_cycle_entry_5097_day":  serviceDay,
			"dungeon_cycle_entry_5097_used": 1,
			// Echon Trial: 5098 cleared once => entries=1, clears=1
			"dungeon_daily_entry_5098_day":  serviceDay,
			"dungeon_daily_entry_5098_used": 1,
			"dungeon_cycle_entry_5098_day":  serviceDay,
			"dungeon_cycle_entry_5098_used": 1,
			// Anton Raid: 2/2 this week + permanent instance dedupe key
			"raid_weekly_epoch_v1_1":                  epoch,
			"raid_weekly_clears_v1_1":                 2,
			"raid_clear_v1_1_0123456789abcdef0123456789abcdef": 1,
			// Luke Raid: 1/2 this week
			"raid_weekly_epoch_v1_2":  epoch,
			"raid_weekly_clears_v1_2": 1,
			// Fiend War: stale previous week counter => should read as 0/2 (canReset=false)
			"raid_weekly_epoch_v1_3":  epoch - 7*24*3600,
			"raid_weekly_clears_v1_3": 2,
		},
	}

	var onlineFlag atomic.Bool
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAccountEditFixture(t, w, r, onlineFlag.Load())
	}))
	defer runtimeSrv.Close()
	rtClient, err := adminapi.NewClient(runtimeSrv.URL+adminapi.Path, strings.Repeat("r", 32))
	if err != nil {
		t.Fatal(err)
	}

	source := dungeonQuotaFixtureSource()
	var catalogQuotaCalls atomic.Int64
	catSrv := &Server{
		cfg:      Config{Password: strings.Repeat("p", 48)},
		deps:     Deps{PVF: func() (dnfpvf.Source, error) { return source, nil }},
		pvf:      newPVFCache(),
		sessions: map[string]time.Time{},
		failures: map[string][]time.Time{},
		grants:   map[string]grantRecord{},
	}
	catSrv.items = newItemIndex(filepath.Join(t.TempDir(), "cat-items.json"), catSrv, nil)
	catSrv.questIndex = &questDiskIndex{Checksum: source.checksum, ManifestHash: strings.Repeat("a", 64)}
	rawCatHandler := catSrv.CatalogHandler(strings.Repeat("c", 48))
	catHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := os.ReadFile(os.DevNull)
		_ = rawBody
		var peek catalogRequest
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		_ = json.Unmarshal(buf.Bytes(), &peek)
		if peek.Operation == "dungeon-quotas" {
			catalogQuotaCalls.Add(1)
		}
		r.Body = http.NoBody
		req2 := r.Clone(r.Context())
		req2.Body = ioReadCloser(buf.Bytes())
		rawCatHandler.ServeHTTP(w, req2)
	}))
	defer catHTTP.Close()

	remote, err := NewRemoteCatalog(servicehttp.Config{URL: catHTTP.URL, Token: strings.Repeat("c", 48), PVFSHA256: source.checksum})
	if err != nil {
		t.Fatal(err)
	}

	settingsStore := &memoryQuotaSettings{settings: map[string]repository.SettingsRecord{}}
	gwSrv := &Server{
		cfg: Config{Password: strings.Repeat("g", 48)},
		deps: Deps{
			Catalog: remote,
			PVF:     func() (dnfpvf.Source, error) { return remote, nil },
			Runtime: rtClient,
			Repos: func() (repository.Group, bool) {
				return repository.Group{
					Character: store,
					Skill:     stubSkillRepo{},
					Inventory: stubInvRepo{},
					Mailbox:   stubMailRepo{},
					Settings:  settingsStore,
				}, true
			},
		},
		pvf:      newPVFCache(),
		quotaNow: func() time.Time { return fixedNow },
		sessions: map[string]time.Time{},
		failures: map[string][]time.Time{},
		grants:   map[string]grantRecord{},
	}
	gwSrv.items = newItemIndex(filepath.Join(t.TempDir(), "gw-items.json"), gwSrv, nil)
	gwSrv.questIndex = &questDiskIndex{Checksum: source.checksum, ManifestHash: strings.Repeat("a", 64)}
	gwHTTP := httptest.NewServer(gwSrv.GatewayHandler(strings.Repeat("k", 48), source.checksum))
	defer gwHTTP.Close()

	peer, err := servicehttp.New(servicehttp.Config{URL: gwHTTP.URL, Token: strings.Repeat("k", 48), PVFSHA256: source.checksum})
	if err != nil {
		t.Fatal(err)
	}
	extHandler := catSrv.ExternalHandler(t.TempDir(), "https://purplemagic.mansus.cc", peer)
	token, err := catSrv.startSession()
	if err != nil {
		t.Fatal(err)
	}
	csrf := catSrv.v1CSRF(&http.Request{Header: http.Header{"Cookie": []string{CookieName + "=" + token}}})

	doExt := func(method, target string, body any, idem string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			var e error
			raw, e = json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
		}
		req := httptest.NewRequest(method, target, bytes.NewReader(raw))
		req.Host = "purplemagic.mansus.cc"
		req.Header.Set("Origin", "https://purplemagic.mansus.cc")
		req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
			if idem != "" {
				req.Header.Set("Idempotency-Key", idem)
			}
		}
		rec := httptest.NewRecorder()
		extHandler.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// 1. Capabilities check through ExternalHandler
	status, capBody := doExt(http.MethodGet, "/api/v1/capabilities", nil, "")
	if status != 200 {
		t.Fatalf("capabilities status=%d body=%v", status, capBody)
	}
	features := capBody["data"].(map[string]any)["features"].(map[string]any)
	if features["dungeonQuotas.read"] != true || features["dungeonQuotas.write"] != true {
		t.Fatalf("expected dungeonQuotas capabilities enabled, got %v", features)
	}

	// 2. GET dungeon-quotas
	status, qBody := doExt(http.MethodGet, "/api/v1/characters/10/dungeon-quotas", nil, "")
	if status != 200 {
		t.Fatalf("get quotas status=%d body=%v", status, qBody)
	}
	qData := qBody["data"].(map[string]any)
	quotasRaw := qData["quotas"].([]any)
	if len(quotasRaw) != 5 {
		t.Fatalf("expected 5 quotas, got %d", len(quotasRaw))
	}
	quotaByKey := func(list []any) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, item := range list {
			entry := item.(map[string]any)
			m[entry["key"].(string)] = entry
		}
		return m
	}
	byKey := quotaByKey(quotasRaw)
	if byKey["pandemonium_rift"]["used"] != float64(2) || byKey["pandemonium_rift"]["canReset"] != true {
		t.Fatalf("unexpected pandemonium_rift: %v", byKey["pandemonium_rift"])
	}
	if byKey["echon"]["used"] != float64(4) || byKey["echon"]["limit"] != float64(4) || byKey["echon"]["templeEntries"] != float64(3) || byKey["echon"]["templeClears"] != float64(2) || byKey["echon"]["trialEntries"] != float64(1) {
		t.Fatalf("unexpected echon: %v", byKey["echon"])
	}
	if byKey["anton_raid"]["used"] != float64(2) || byKey["luke_raid"]["used"] != float64(1) {
		t.Fatalf("unexpected anton/luke: %v %v", byKey["anton_raid"], byKey["luke_raid"])
	}
	if byKey["fiend_war"]["used"] != float64(0) || byKey["fiend_war"]["canReset"] != false {
		t.Fatalf("expected stale fiend_war to report 0 used, got %v", byKey["fiend_war"])
	}

	// 3. Zero-count guard on fiend_war (0 used) -> 422 quota_not_used
	status, errBody := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations/preview", map[string]any{
		"accountId":   "dnf:1001",
		"action":      "reset",
		"quotaKey":    "fiend_war",
		"revision":    qData["revision"],
		"pvfChecksum": qData["pvfChecksum"],
		"reason":      "测试零次数拦截",
	}, "")
	if status != 422 || errBody["error"].(map[string]any)["code"] != "quota_not_used" {
		t.Fatalf("expected 422 quota_not_used, got %d %v", status, errBody)
	}

	// 4. Preview + Commit reset on echon (clears all 4 dungeon IDs across daily_entry & cycle_entry)
	previewReq := map[string]any{
		"accountId":          "dnf:1001",
		"action":             "reset",
		"quotaKey":           "echon",
		"revision":           qData["revision"],
		"pvfChecksum":        qData["pvfChecksum"],
		"definitionRevision": qData["definitionRevision"],
		"reason":             "重置艾肯",
	}
	status, pBody := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations/preview", previewReq, "")
	if status != 200 {
		t.Fatalf("echon preview failed: %d %v", status, pBody)
	}
	pData := pBody["data"].(map[string]any)
	commitReq := map[string]any{
		"accountId":          "dnf:1001",
		"action":             "reset",
		"quotaKey":           "echon",
		"revision":           qData["revision"],
		"pvfChecksum":        qData["pvfChecksum"],
		"definitionRevision": pData["definitionRevision"],
		"planToken":          pData["planToken"],
		"reason":             "重置艾肯",
	}
	status, cBody := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations", commitReq, "idem-echon-reset-0001")
	if status != 200 {
		t.Fatalf("echon commit failed: %d %v", status, cBody)
	}
	receipt1 := cBody["data"].(map[string]any)
	afterEchon := quotaByKey(receipt1["quotas"].([]any))["echon"]
	if afterEchon["used"] != float64(0) || afterEchon["canReset"] != false || afterEchon["templeEntries"] != float64(0) || afterEchon["trialEntries"] != float64(0) {
		t.Fatalf("echon not reset cleanly: %v", afterEchon)
	}

	// 5. Online character guard & Carbon mode override when resetting anton_raid
	onlineFlag.Store(true)
	antonPreview := map[string]any{
		"accountId":          "dnf:1001",
		"action":             "reset",
		"quotaKey":           "anton_raid",
		"revision":           receipt1["revision"],
		"pvfChecksum":        receipt1["pvfChecksum"],
		"definitionRevision": receipt1["definitionRevision"],
		"reason":             "重置安徒恩",
	}
	status, p2Body := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations/preview", antonPreview, "")
	if status != 200 {
		t.Fatalf("anton preview failed: %d %v", status, p2Body)
	}
	antonCommit := map[string]any{
		"accountId":          "dnf:1001",
		"action":             "reset",
		"quotaKey":           "anton_raid",
		"revision":           receipt1["revision"],
		"pvfChecksum":        receipt1["pvfChecksum"],
		"definitionRevision": p2Body["data"].(map[string]any)["definitionRevision"],
		"planToken":          p2Body["data"].(map[string]any)["planToken"],
		"reason":             "重置安徒恩",
	}
	status, onlineErr := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations", antonCommit, "idem-anton-reset-0002")
	if status != 409 || onlineErr["error"].(map[string]any)["code"] != "character_online" {
		t.Fatalf("expected 409 character_online, got %d %v", status, onlineErr)
	}
	// Enable carbonMode via /api/v1/settings/system
	status, sysBody := doExt(http.MethodPost, "/api/v1/settings/system", map[string]any{"carbonMode": true}, "")
	if status != 200 {
		t.Fatalf("enable carbon mode failed: %d %v", status, sysBody)
	}
	// Simulate go-server routine online save updating charac_info.updated_at between preview and commit.
	store.mu.Lock()
	onlineSaved := repository.CloneCharacter(store.chars["10"])
	onlineSaved.UpdatedAt = fixedNow.Add(15 * time.Second)
	store.chars["10"] = onlineSaved
	store.mu.Unlock()

	status, c2Body := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations", antonCommit, "idem-anton-reset-0002")
	if status != 200 {
		t.Fatalf("anton commit with carbonMode after online UpdatedAt advance failed: %d %v", status, c2Body)
	}
	// Verify permanent instance dedupe key & Luke raid & gold were untouched!
	persisted := store.chars["10"]
	if persisted.Stats["raid_clear_v1_1_0123456789abcdef0123456789abcdef"] != 1 || persisted.Stats["raid_weekly_clears_v1_2"] != 1 || persisted.Stats["gold"] != 123456 {
		t.Fatalf("unrelated stats mutated: %v", persisted.Stats)
	}
	if _, hasEpoch := persisted.Stats["raid_weekly_epoch_v1_1"]; hasEpoch {
		t.Fatalf("anton weekly epoch still present: %v", persisted.Stats)
	}

	// Verify actual quota stat drift between preview and commit still returns 409 revision_conflict.
	receipt2 := c2Body["data"].(map[string]any)
	riftPreview := map[string]any{
		"accountId":          "dnf:1001",
		"action":             "reset",
		"quotaKey":           "pandemonium_rift",
		"revision":           receipt2["revision"],
		"pvfChecksum":        receipt2["pvfChecksum"],
		"definitionRevision": receipt2["definitionRevision"],
		"reason":             "重置魔界裂缝",
	}
	status, p3Body := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations/preview", riftPreview, "")
	if status != 200 {
		t.Fatalf("rift preview failed: %d %v", status, p3Body)
	}
	store.mu.Lock()
	riftDrifted := repository.CloneCharacter(store.chars["10"])
	riftDrifted.Stats["dungeon_daily_entry_316_used"] = 3
	store.chars["10"] = riftDrifted
	store.mu.Unlock()
	status, conflictErr := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations", map[string]any{
		"accountId":          "dnf:1001",
		"action":             "reset",
		"quotaKey":           "pandemonium_rift",
		"revision":           receipt2["revision"],
		"pvfChecksum":        receipt2["pvfChecksum"],
		"definitionRevision": p3Body["data"].(map[string]any)["definitionRevision"],
		"planToken":          p3Body["data"].(map[string]any)["planToken"],
		"reason":             "重置魔界裂缝",
	}, "idem-rift-conflict-0003")
	if status != 409 || conflictErr["error"].(map[string]any)["code"] != "revision_conflict" {
		t.Fatalf("expected 409 revision_conflict when managed quota stat changes, got %d %v", status, conflictErr)
	}

	// 6. RemoteCatalog cached the static quota catalog in 1 call!
	if got := catalogQuotaCalls.Load(); got != 1 {
		t.Fatalf("expected RemoteCatalog to fetch dungeon-quotas exactly once, got %d", got)
	}

	// 7. Lost-response replay works even after used == 0 and Runtime is nil!
	gwSrv.deps.Runtime = nil
	status, replayBody := doExt(http.MethodPost, "/api/v1/characters/10/dungeon-quota-operations", antonCommit, "idem-anton-reset-0002")
	if status != 200 || !reflect.DeepEqual(c2Body["data"], replayBody["data"]) {
		t.Fatalf("replay mismatch: %d %v vs %v", status, replayBody, c2Body)
	}
}

func ioReadCloser(b []byte) *nopCloserBuffer {
	return &nopCloserBuffer{Reader: bytes.NewReader(b)}
}

type nopCloserBuffer struct {
	*bytes.Reader
}

func (nopCloserBuffer) Close() error { return nil }

func TestRealPVFDungeonQuotaCatalog(t *testing.T) {
	pvfPath := os.Getenv("DNFBRIDGE_REAL_PVF_SMOKE")
	if pvfPath == "" {
		const localCandidate = `C:\BDown\client\client\Script.pvf`
		if _, err := os.Stat(localCandidate); err == nil {
			pvfPath = localCandidate
		} else {
			t.Skip("real Script.pvf not present locally")
		}
	}
	archive, err := platformpvf.LoadArchive(platformpvf.Options{Path: pvfPath})
	if err != nil {
		t.Fatalf("load real PVF: %v", err)
	}
	snap := archive.Snapshot()
	cat, err := adminops.LoadDungeonQuotaCatalog(archive, snap.Checksum)
	if err != nil {
		t.Fatalf("LoadDungeonQuotaCatalog failed on real PVF: %v", err)
	}
	if !slices.Equal(cat.EchonTempleIDs, []int64{3570, 5097}) || cat.EchonTempleLimit != 3 ||
		!slices.Equal(cat.EchonTrialIDs, []int64{3571, 5098}) || cat.EchonTrialLimit != 1 ||
		cat.AntonWeeklyLimit != 2 || cat.LukeWeeklyLimit != 2 || cat.FiendWeeklyLimit != 2 {
		t.Fatalf("unexpected real PVF catalog: %+v", cat)
	}
}

type quotaFailStore struct {
	repository.DungeonQuotaManagementRepository
}

func (s quotaFailStore) ResetDungeonQuotaKeys(ctx context.Context, characterID string, deleteKeys []string, sequence int64, updatedAt time.Time) error {
	if err := s.DungeonQuotaManagementRepository.ResetDungeonQuotaKeys(ctx, characterID, deleteKeys, sequence, updatedAt); err != nil {
		return err
	}
	return errors.New("injected quota save failure")
}

type quotaFailUOW struct {
	repository.DungeonQuotaManagementUnitOfWork
	save bool
}

func (s quotaFailUOW) WithinDungeonQuotaManagement(ctx context.Context, a, id string, fn func(repository.DungeonQuotaManagementRepository, repository.ManagementReceiptStore) error) error {
	return s.DungeonQuotaManagementUnitOfWork.WithinDungeonQuotaManagement(ctx, a, id, func(r repository.DungeonQuotaManagementRepository, receipt repository.ManagementReceiptStore) error {
		if s.save {
			r = quotaFailStore{r}
		} else {
			receipt = failingReceipts{receipt}
		}
		return fn(r, receipt)
	})
}

func TestV1DungeonQuotasMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			ctx := context.Background()
			s, _, open := currencyFixture(t, shards)
			g := open()
			check := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			fixedNow := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
			serviceDay := premium.ServiceDayID(fixedNow)
			policy, e := adminops.DefaultDungeonQuotaWeeklyPolicy()
			check(e)
			epoch, e := raid.WeeklyEpoch(fixedNow, policy)
			check(e)

			source := dungeonQuotaFixtureSource()
			s.pvf = newPVFCache()
			s.quotaNow = func() time.Time { return fixedNow }
			s.items = newItemIndex(filepath.Join(t.TempDir(), "items.json"), s, nil)
			s.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }

			for i, id := range []string{"10", "11", "20", "21"} {
				a := "wallet:1"
				if i > 1 {
					a = "wallet:2"
				}
				check(g.Character.Save(ctx, repository.CharacterRecord{
					CharacterID: id,
					AccountID:   a,
					Slot:        i,
					Name:        "额度" + id,
					Job:         "0",
					Level:       90,
					Stats: map[string]int64{
						"gold":                                             888,
						"keep":                                             87,
						"dungeon_daily_entry_316_day":                      serviceDay,
						"dungeon_daily_entry_316_used":                     3,
						"dungeon_daily_entry_3570_day":                     serviceDay,
						"dungeon_daily_entry_3570_used":                    2,
						"dungeon_cycle_entry_3570_day":                     serviceDay,
						"dungeon_cycle_entry_3570_used":                    3,
						"raid_weekly_epoch_v1_1":                           epoch,
						"raid_weekly_clears_v1_1":                          2,
						"raid_clear_v1_1_fedcba9876543210fedcba9876543210": 1,
						"raid_weekly_epoch_v1_2":                           epoch,
						"raid_weekly_clears_v1_2":                          1,
						"raid_weekly_epoch_v1_3":                           epoch,
						"raid_weekly_clears_v1_3":                          2,
					},
				}))
			}

			u := g.Character.(repository.DungeonQuotaManagementUnitOfWork)
			reader := g.Character.(repository.DungeonQuotaManagementReader)
			cat, e := s.dungeonQuotaCatalog(ctx)
			check(e)
			read := func(id string) repository.DungeonQuotaManagementState {
				v, err := reader.LoadDungeonQuotaManagement(ctx, id)
				check(err)
				return v
			}
			full := func(id string) repository.CharacterRecord {
				c, ok, err := g.Character.Load(ctx, id)
				check(err)
				if !ok {
					t.Fatal("missing character", id)
				}
				return c
			}
			offline := func(context.Context, string) error { return nil }
			prepare := func(id, quotaKey string) adminops.DungeonQuotaCommand {
				v := read(id)
				cmd := adminops.DungeonQuotaCommand{
					AccountID:   v.Character.AccountID,
					CharacterID: id,
					Action:      "reset",
					QuotaKey:    quotaKey,
					Revision:    adminops.DungeonQuotaRevision(v, cat, fixedNow, policy),
					PVFChecksum: source.checksum,
					Reason:      "额度重置测试",
					Actor:       "管理员",
					Key:         fmt.Sprintf("quota-%s-%s-%d", id, quotaKey, time.Now().UnixNano()),
				}
				p, err := adminops.RunDungeonQuotaOperation(ctx, u, cmd, false, fixedNow, policy, s.dungeonQuotaCatalog, nil)
				check(err)
				cmd.DefinitionRevision = p.DefinitionRevision
				return cmd
			}

			before := full("10")
			other := full("11")
			c := prepare("10", "pandemonium_rift")
			unchanged := func() {
				t.Helper()
				if !reflect.DeepEqual(full("10"), before) {
					t.Fatal("failed transaction mutated character")
				}
				rows, err := u.DungeonQuotaManagementLog(ctx, "10", 0, 100)
				check(err)
				if len(rows) != 0 {
					t.Fatal("unexpected receipt row")
				}
			}
			unchanged()

			for _, mode := range []string{"save", "receipt", "offline", "late-online"} {
				worker := u
				off := offline
				switch mode {
				case "save":
					worker = quotaFailUOW{u, true}
				case "receipt":
					worker = quotaFailUOW{u, false}
				case "offline":
					off = nil
				case "late-online":
					calls := 0
					off = func(context.Context, string) error {
						calls++
						if calls == 2 {
							return adminops.ErrCharacterOnline
						}
						return nil
					}
				}
				if _, e = adminops.RunDungeonQuotaOperation(ctx, worker, c, true, fixedNow, policy, s.dungeonQuotaCatalog, off); e == nil {
					t.Fatal("expected failure for mode", mode)
				}
				unchanged()
			}

			// Concurrent execution with identical Idempotency-Key commits once cleanly
			var wg sync.WaitGroup
			errs := make(chan error, 6)
			for range 6 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := adminops.RunDungeonQuotaOperation(ctx, u, c, true, fixedNow, policy, s.dungeonQuotaCatalog, offline)
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				check(err)
			}

			after := full("10")
			if _, exists := after.Stats["dungeon_daily_entry_316_used"]; exists {
				t.Fatal("pandemonium_rift used stat remained", after.Stats)
			}
			if after.Stats["gm_dungeon_quota_sequence_v1"] != 1 || after.Stats["gold"] != 888 || after.Stats["keep"] != 87 || after.Stats["raid_clear_v1_1_fedcba9876543210fedcba9876543210"] != 1 {
				t.Fatal("unexpected stats after reset", after.Stats)
			}
			if !reflect.DeepEqual(full("11"), other) {
				t.Fatal("other character mutated")
			}

			// Idempotency conflict with mutated quotaKey
			bad := c
			bad.QuotaKey = "echon"
			if _, e = adminops.RunDungeonQuotaOperation(ctx, u, bad, true, fixedNow, policy, s.dungeonQuotaCatalog, offline); !errors.Is(e, adminops.ErrIdempotency) {
				t.Fatal("expected ErrIdempotency, got", e)
			}

			// Stale revision rejected
			stale := c
			stale.Key = "quota-stale-request-0001"
			if _, e = adminops.RunDungeonQuotaOperation(ctx, u, stale, true, fixedNow, policy, s.dungeonQuotaCatalog, offline); !errors.Is(e, adminops.ErrConflict) {
				t.Fatal("expected ErrConflict, got", e)
			}

			// HTTP preview + tamper + commit + log cursor isolation
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeAccountEditFixture(t, w, r, false)
			}))
			defer runtime.Close()
			s.deps.Runtime, e = adminapi.NewClient(runtime.URL+adminapi.Path, strings.Repeat("x", 32))
			check(e)
			token, e := s.startSession()
			check(e)

			status, body := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/dungeon-quotas")
			if status != 200 {
				t.Fatal(status, body)
			}
			payload := map[string]any{
				"accountId":   "wallet:1",
				"action":      "reset",
				"quotaKey":    "fiend_war",
				"revision":    body["data"].(map[string]any)["revision"],
				"pvfChecksum": source.checksum,
				"reason":      "HTTP超时空重置",
			}
			call := func(path, key string, headers map[string]string) (int, map[string]any) {
				raw, _ := json.Marshal(payload)
				return operationCall(s, token, path, string(raw), key, headers)
			}
			path := "characters/10/dungeon-quota-operations"
			status, body = call(path+"/preview", "", nil)
			if status != 200 {
				t.Fatal(status, body)
			}
			data := body["data"].(map[string]any)
			payload["planToken"] = data["planToken"]
			payload["definitionRevision"] = data["definitionRevision"]
			payload["quotaKey"] = "luke_raid"
			if status, _ = call(path, "quota-http-operation-001", nil); status != 409 {
				t.Fatal("tampered quotaKey accepted", status)
			}
			payload["quotaKey"] = "fiend_war"
			status, body = call(path, "quota-http-operation-001", nil)
			if status != 200 {
				t.Fatal(status, body)
			}
			receipt := body["data"]
			s.deps.PVF = nil
			s.deps.Runtime = nil
			status, body = call(path, "quota-http-operation-001", nil)
			if status != 200 || !reflect.DeepEqual(receipt, body["data"]) {
				t.Fatal("durable HTTP replay failed", status, body)
			}

			status, body = v1TestCall(t, s, token, "GET", "/api/v1/"+path+"?limit=1")
			if status != 200 {
				t.Fatal(status, body)
			}
			cursor := url.QueryEscape(body["page"].(map[string]any)["nextCursor"].(string))
			if status, _ = v1TestCall(t, s, token, "GET", "/api/v1/characters/11/dungeon-quota-operations?limit=1&cursor="+cursor); status != 400 {
				t.Fatal("cursor isolation failed", status)
			}
		})
	}
}
