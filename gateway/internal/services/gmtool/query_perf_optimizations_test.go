package gmtool

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	repomysql "dnf90.local/gateway/internal/modules/dnf/repository/mysql"
	"dnf90.local/gateway/internal/servicehttp"
)

func TestRemoteCatalogChunkedPreloadStaticCachingAndGenerationSwitch(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	currentHash := strings.Repeat("1", 64)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := currentHash
		mu.Unlock()

		servicehttp.Protect(strings.Repeat("t", 32), h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req catalogRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			calls[req.Operation]++
			mu.Unlock()

			var val any
			switch req.Operation {
			case "metadata":
				val = map[string]string{"ManifestHash": h}
			case "items":
				if len(req.ItemIDs) > 200 {
					http.Error(w, "chunk exceeded 200", 400)
					return
				}
				rows := make([]catalogItem, 0, len(req.ItemIDs))
				for _, id := range req.ItemIDs {
					rows = append(rows, catalogItem{Entry: itemEntry{ID: id, Name: fmt.Sprintf("item-%d", id)}, Found: true})
				}
				val = rows
			case "quests":
				if len(req.QuestIDs) > 200 {
					http.Error(w, "quest chunk exceeded 200", 400)
					return
				}
				rows := make([]catalogQuest, 0, len(req.QuestIDs))
				for _, id := range req.QuestIDs {
					rows = append(rows, catalogQuest{
						Row:        questIndexRow{ID: id, Name: fmt.Sprintf("quest-%d", id), Path: "n_quest/q.qst"},
						Definition: quest.Definition{ID: id, Name: fmt.Sprintf("quest-%d", id), Path: "n_quest/q.qst"},
						Found:      true,
					})
				}
				val = rows
			case "level":
				val = adminops.LevelDefinition{PVFChecksum: h, Level: req.Level, MinimumExperience: 12345}
			case "skill-points":
				val = adminops.SkillPointDefinition{PVFChecksum: h, Level: req.Level}
			case "expert":
				val = map[string]any{"pvfChecksum": h, "jobs": []any{}}
			case "vault":
				val = adminops.VaultDefinition{Revision: h, Tiers: []adminops.VaultTier{{Capacity: 8, GoldLimit: 1000000}}}
			case "cube":
				val = map[string]any{"id": req.ItemID, "name": "无色小晶块"}
			default:
				http.Error(w, "unexpected op", 400)
				return
			}
			raw, _ := json.Marshal(val)
			_ = json.NewEncoder(w).Encode(catalogResult{Value: raw})
		})).ServeHTTP(w, r)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	catalog, err := NewRemoteCatalog(servicehttp.Config{
		URL:       srv.URL,
		Token:     strings.Repeat("t", 32),
		PVFSHA256: currentHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 1. Preload 450 distinct items -> must split into 3 chunks (200 + 200 + 50), then 0 RPCs on repeat!
	ids := make([]uint32, 450)
	for i := range ids {
		ids[i] = uint32(i + 1)
	}
	if err := catalog.preload(ctx, ids); err != nil {
		t.Fatalf("preload 450 items failed: %v", err)
	}
	if err := catalog.preload(ctx, ids); err != nil {
		t.Fatalf("second preload failed: %v", err)
	}

	// 2. Load 250 quests -> must split into 2 chunks (200 + 50), then 0 quest RPCs on a new questIndex!
	qids := make([]int64, 250)
	for i := range qids {
		qids[i] = int64(i + 1)
	}
	ix1, err := catalog.questIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix1.remoteLoad(ctx, qids); err != nil {
		t.Fatalf("remoteLoad 250 quests failed: %v", err)
	}
	ix2, err := catalog.questIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix2.remoteLoad(ctx, qids); err != nil {
		t.Fatal(err)
	}
	if def, err := ix2.definition(ctx, 249); err != nil || def.Name != "quest-249" {
		t.Fatalf("cached quest definition mismatch: %+v, err=%v", def, err)
	}

	// 3. Static operations: level, skill-points, expert, vault, cube -> cached across concurrent/repeated calls!
	for range 5 {
		var lvl adminops.LevelDefinition
		if err := catalog.call(ctx, catalogRequest{Operation: "level", CharacterJob: "0", Grow: 1, Level: 90}, &lvl); err != nil || lvl.MinimumExperience != 12345 {
			t.Fatal(lvl, err)
		}
		var sp adminops.SkillPointDefinition
		if err := catalog.call(ctx, catalogRequest{Operation: "skill-points", Level: 90}, &sp); err != nil || sp.Level != 90 {
			t.Fatal(sp, err)
		}
		var exp map[string]any
		if err := catalog.call(ctx, catalogRequest{Operation: "expert"}, &exp); err != nil {
			t.Fatal(err)
		}
		var vlt adminops.VaultDefinition
		if err := catalog.call(ctx, catalogRequest{Operation: "vault"}, &vlt); err != nil || len(vlt.Tiers) != 1 {
			t.Fatal(vlt, err)
		}
		var cb map[string]any
		if err := catalog.call(ctx, catalogRequest{Operation: "cube", ItemID: 3037}, &cb); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	if calls["items"] != 3 || calls["quests"] != 2 || calls["metadata"] != 1 || calls["level"] != 1 || calls["skill-points"] != 1 || calls["expert"] != 1 || calls["vault"] != 1 || calls["cube"] != 1 {
		mu.Unlock()
		t.Fatalf("unexpected RPC counts before generation switch: %+v", calls)
	}

	// 4. Switch PVF generation -> caches must flush and re-fetch cleanly!
	nextHash := strings.Repeat("2", 64)
	currentHash = nextHash
	mu.Unlock()
	nextPeer, err := servicehttp.New(servicehttp.Config{
		URL:       srv.URL,
		Token:     strings.Repeat("t", 32),
		PVFSHA256: nextHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog.peer = nextPeer

	var vltAfter adminops.VaultDefinition
	if err := catalog.call(ctx, catalogRequest{Operation: "vault"}, &vltAfter); err != nil || vltAfter.Revision != nextHash {
		t.Fatalf("stale vault definition after generation switch: %+v, err=%v", vltAfter, err)
	}
	if err := catalog.preload(ctx, []uint32{1}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["vault"] != 2 || calls["items"] != 4 {
		t.Fatalf("expected generation switch to trigger fresh fetches, got %+v", calls)
	}
}

type mockPerfSQLState struct {
	mu               sync.Mutex
	queries          []string
	manySlotChars    int
	corruptReceiptOp bool
}

type mockPerfConnector struct{ s *mockPerfSQLState }

func (c mockPerfConnector) Connect(context.Context) (driver.Conn, error) {
	return mockPerfConn{c.s}, nil
}
func (c mockPerfConnector) Driver() driver.Driver { return mockPerfDriver{c.s} }

type mockPerfDriver struct{ s *mockPerfSQLState }

func (d mockPerfDriver) Open(string) (driver.Conn, error) { return mockPerfConn{d.s}, nil }

type mockPerfConn struct{ s *mockPerfSQLState }

func (c mockPerfConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected prepare")
}
func (c mockPerfConn) Close() error              { return nil }
func (c mockPerfConn) Begin() (driver.Tx, error) { return mockPerfTx{}, nil }
func (c mockPerfConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return mockPerfTx{}, nil
}

type mockPerfTx struct{}

func (mockPerfTx) Commit() error   { return nil }
func (mockPerfTx) Rollback() error { return nil }

func (c mockPerfConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	c.s.queries = append(c.s.queries, q)
	many := c.s.manySlotChars
	corruptOp := c.s.corruptReceiptOp
	c.s.mu.Unlock()

	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	// Registration DB queries.
	if strings.Contains(q, "FROM `web_accounts`") {
		if strings.Contains(q, "EXISTS(SELECT 1 FROM `web_accounts_bans`") {
			return &mockPerfRows{cols: 3, vals: [][]driver.Value{
				{"acc:1", "login_one", int64(0)},
				{"acc:2", "login_two", int64(1)},
			}}, nil
		}
		if strings.HasPrefix(q, "SELECT a.account_id,a.account,COALESCE(p.disabled,0)") {
			return &mockPerfRows{cols: 3, vals: [][]driver.Value{
				{"acc:1", "login_one", int64(0)},
			}}, nil
		}
		if strings.HasPrefix(q, "SELECT a.id,a.account_id,a.account") {
			return &mockPerfRows{cols: 6, vals: [][]driver.Value{
				{int64(1), "acc:1", "login_one", int64(0), now, now},
			}}, nil
		}
		return &mockPerfRows{cols: 3, vals: [][]driver.Value{
			{"acc:1", "login_one", int64(0)},
			{"acc:2", "login_two", int64(0)},
		}}, nil
	}

	// AccountIDsByCharacterQuery
	if strings.HasPrefix(q, "SELECT character_id,account_id FROM ") {
		return &mockPerfRows{cols: 2, vals: [][]driver.Value{{"10", "acc:1"}}}, nil
	}

	// SummariesForAccounts / SearchSummaries / FindSummary on dnf_characters
	if strings.Contains(q, "dnf_characters") {
		if strings.Contains(q, "account_id IN") && many > 0 {
			vals := make([][]driver.Value, 0, many)
			for i := 0; i < many; i++ {
				vals = append(vals, []driver.Value{
					fmt.Sprintf("%d", 100+i), "acc:1", fmt.Sprintf("角色%d", i), "0", int64(90), int64(1), int64(i), now, now,
				})
			}
			return &mockPerfRows{cols: 9, vals: vals}, nil
		}
		return &mockPerfRows{cols: 9, vals: [][]driver.Value{
			{"10", "acc:1", "红眼大号", "0", int64(90), int64(2), int64(0), now, now},
		}}, nil
	}

	// InventoryBatch merged query across 6 bag tabs
	if strings.Contains(q, "dnf_inventory_items") && !strings.Contains(q, "OCTET_LENGTH(raw_entry)") {
		return &mockPerfRows{cols: 6, vals: [][]driver.Value{
			{"0:3", int64(3), int64(1), int64(5), int64(0), nil},     // quickbar
			{"0:9", int64(9), int64(1), int64(1), int64(1), nil},     // equipment
			{"0:65", int64(65), int64(1), int64(20), int64(0), nil},  // consumables
			{"0:121", int64(121), int64(1), int64(9), int64(0), nil}, // materials
			{"0:177", int64(177), int64(1), int64(2), int64(0), nil}, // quest-items
			{"0:233", int64(233), int64(1), int64(7), int64(0), nil}, // profession
		}}, nil
	}

	// InventoryPage detail with oversized 10000-byte raw_entry & >256 extra keys
	if strings.Contains(q, "dnf_inventory_items") && strings.Contains(q, "OCTET_LENGTH(raw_entry)") {
		return &mockPerfRows{cols: 8, vals: [][]driver.Value{
			{"0:9", int64(9), int64(1), int64(1), int64(1), nil, nil, int64(10000)},
		}}, nil
	}
	if strings.Contains(q, "dnf_inventory_item_extra") {
		vals := make([][]driver.Value, 300)
		for i := range vals {
			vals[i] = []driver.Value{fmt.Sprintf("k%d", i), strings.Repeat("v", 5000)}
		}
		return &mockPerfRows{cols: 2, vals: vals}, nil
	}

	// MailDetail with 100000-byte body & 120 attachments
	if strings.Contains(q, "dnf_mails") && strings.Contains(q, "LEFT(body,262144)") {
		return &mockPerfRows{cols: 9, vals: [][]driver.Value{
			{"m1", "GM", "大邮件", strings.Repeat("正文", 40000), int64(100), int64(0), int64(0), now, nil},
		}}, nil
	}
	if strings.Contains(q, "dnf_mail_attachments") {
		vals := make([][]driver.Value, 120)
		for i := range vals {
			vals[i] = []driver.Value{int64(1), int64(1), int64(0), nil}
		}
		return &mockPerfRows{cols: 4, vals: vals}, nil
	}

	// Operation logs with a corrupt JSON row mixed with a valid receipt row
	if corruptOp && (strings.Contains(q, "profession_management_operations") || strings.Contains(q, "dungeon_quota_management_operations")) {
		validRow := `{"receipt":{"kind":"profession-operation","accountId":"acc:1","characterId":"10","reason":"ok","actor":"GM"},"contentHash":"abc"}`
		if strings.Contains(q, "dungeon_quota_management_operations") {
			validRow = `{"receipt":{"kind":"dungeon-quota-operation","accountId":"acc:1","characterId":"10","quotaKey":"anton_raid","reason":"ok","actor":"GM"},"contentHash":"abc"}`
		}
		return &mockPerfRows{cols: 2, vals: [][]driver.Value{
			{int64(2), "{corrupt-json"},
			{int64(1), validRow},
		}}, nil
	}

	if strings.Contains(q, "dnf_account_metadata") || strings.Contains(q, "account_cera") {
		return &mockPerfRows{cols: 0}, nil
	}
	return &mockPerfRows{cols: 0}, nil
}

type mockPerfRows struct {
	cols int
	idx  int
	vals [][]driver.Value
}

func (r *mockPerfRows) Columns() []string {
	out := make([]string, r.cols)
	for i := range out {
		out[i] = fmt.Sprintf("c%d", i)
	}
	return out
}
func (r *mockPerfRows) Close() error { return nil }
func (r *mockPerfRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.idx])
	r.idx++
	return nil
}

func TestInventoryBatchUnlimitedSlotsToleranceDirectoryAndBanCache(t *testing.T) {
	state := &mockPerfSQLState{manySlotChars: 40, corruptReceiptOp: true}
	rawDB := sql.OpenDB(mockPerfConnector{s: state})
	defer rawDB.Close()

	group, err := repomysql.NewMySQLGroup(repomysql.SQLDBAdapter{DB: rawDB}, repomysql.MySQLGroupOptions{
		DatabasePlan: repository.DatabasePlan{
			ReadDatabases:  []string{"shard0"},
			WriteDatabases: []string{"shard0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	source := newItemCacheTestSource(readonlyTestSource{
		"stackable/stackable.lst": "1 `stackable/potion.stk`\n",
		"stackable/potion.stk":    "[name]\n`雷米援助`\n",
	})
	s := &Server{
		cfg:      Config{Table: "web_accounts", Password: strings.Repeat("p", 32), Logger: func(string, ...any) {}},
		db:       rawDB,
		deps:     Deps{Repos: func() (repository.Group, bool) { return group, true }, PVF: func() (dnfpvf.Source, error) { return source, nil }},
		www:      http.NotFoundHandler(),
		sessions: map[string]time.Time{},
		failures: map[string][]time.Time{},
		pvf:      newPVFCache(),
	}
	s.items = &itemIndex{
		ready:   true,
		entries: []itemEntry{{ID: 1, Name: "雷米援助", Kind: "stackable"}},
		byID:    map[uint32]int{1: 0},
	}
	token, err := s.startSession()
	if err != nil {
		t.Fatal(err)
	}

	// 1. SummariesForAccounts with 40 characters (> DefaultCharacterSlotLimit=32) succeeds with 200!
	status, accOut := v1TestCall(t, s, token, "GET", "/api/v1/accounts?q=红眼大号")
	if status != 200 {
		t.Fatalf("GET /api/v1/accounts?q=红眼大号 failed (%d): %v", status, accOut)
	}
	accList := accOut["data"].([]any)
	if len(accList) != 1 || accList[0].(map[string]any)["characterCount"] != float64(40) {
		t.Fatalf("expected 40 characters without ErrCharacterSummaryRange, got %v", accList)
	}
	// Confirm character-name -> account_id lookup ran when q != "".
	state.mu.Lock()
	sawCharLookup := false
	for _, q := range state.queries {
		if strings.HasPrefix(q, "SELECT character_id,account_id FROM ") {
			sawCharLookup = true
		}
	}
	state.queries = nil
	state.mu.Unlock()
	if !sawCharLookup {
		t.Fatal("expected AccountIDsByCharacterQuery to execute on /api/v1/accounts?q=...")
	}

	// 2. Character directory PK scan (no BINARY character_id > ?, no LIKE '%%' when q=="") + accountName enrichment!
	status, charOut := v1TestCall(t, s, token, "GET", "/api/v1/characters?limit=20")
	if status != 200 {
		t.Fatalf("GET /api/v1/characters failed (%d): %v", status, charOut)
	}
	chars := charOut["data"].([]any)
	if len(chars) != 1 || chars[0].(map[string]any)["accountName"] != "login_one" {
		t.Fatalf("expected accountName enrichment on /api/v1/characters, got %v", chars)
	}
	state.mu.Lock()
	for _, q := range state.queries {
		if strings.Contains(q, "dnf_characters") {
			if strings.Contains(q, "BINARY character_id") || strings.Contains(q, "LIKE") {
				state.mu.Unlock()
				t.Fatalf("expected clean PK scan without BINARY or empty LIKE, got: %s", q)
			}
		}
	}
	state.queries = nil
	state.mu.Unlock()

	// 3. Batch inventory endpoint: 6 bag tabs read in ONE SINGLE dnf_inventory_items query, plus ExternalHandler forwarding!
	status, batchOut := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/inventory-batch?containers=equipment,consumables,materials,quest-items,profession,quickbar")
	if status != 200 {
		t.Fatalf("inventory-batch failed (%d): %v", status, batchOut)
	}
	containersMap := batchOut["data"].(map[string]any)["containers"].(map[string]any)
	for _, tab := range []string{"equipment", "consumables", "materials", "quest-items", "profession", "quickbar"} {
		entry, ok := containersMap[tab].(map[string]any)
		if !ok || len(entry["items"].([]any)) != 1 {
			t.Fatalf("missing or empty tab %s in inventory-batch: %v", tab, containersMap)
		}
	}
	state.mu.Lock()
	invQueries := 0
	for _, q := range state.queries {
		if strings.Contains(q, "dnf_inventory_items") {
			invQueries++
		}
	}
	state.mu.Unlock()
	if invQueries != 1 {
		t.Fatalf("expected 1 merged SQL query across 6 bag tabs, got %d", invQueries)
	}

	// Also verify ExternalHandler -> GatewayHandler forwards GET /api/v1/characters/10/inventory-batch seamlessly.
	gwSecret := strings.Repeat("g", 48)
	gwSrv := httptest.NewServer(s.GatewayHandler(gwSecret, s.v1PVFChecksum()))
	defer gwSrv.Close()
	peer, err := servicehttp.New(servicehttp.Config{URL: gwSrv.URL, Token: gwSecret, PVFSHA256: s.v1PVFChecksum()})
	if err != nil {
		t.Fatal(err)
	}
	extHandler := s.ExternalHandler("", "", peer)
	req := httptest.NewRequest("GET", "/api/v1/characters/10/inventory-batch", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	rec := httptest.NewRecorder()
	extHandler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ExternalHandler failed to proxy inventory-batch (%d): %s", rec.Code, rec.Body.String())
	}

	// 4. Oversized raw_entry / extra_value / mail body / attachments tolerate edge data without 503 self-destruct!
	if code, out := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/inventory/equipment/9"); code != 200 {
		t.Fatalf("oversized item detail failed (%d): %v", code, out)
	}
	if code, out := v1TestCall(t, s, token, "GET", "/api/v1/characters/10/mailbox/m1"); code != 200 {
		t.Fatalf("oversized mail detail failed (%d): %v", code, out)
	}

	// 5. Corrupt historical receipt JSON row in profession-operations & dungeon-quota-operations skipped cleanly!
	for _, logPath := range []string{
		"/api/v1/characters/10/profession-operations",
		"/api/v1/characters/10/dungeon-quota-operations",
	} {
		code, out := v1TestCall(t, s, token, "GET", logPath)
		if code != 200 || len(out["data"].([]any)) != 1 {
			t.Fatalf("%s failed to skip single corrupt receipt row (%d): %v", logPath, code, out)
		}
	}

	// 6. Moderation account cache TTL + immediate invalidation on POST /api/v1/anti-cheat/bans!
	state.mu.Lock()
	state.queries = nil
	state.mu.Unlock()
	for range 3 {
		_, excluded, err := s.moderationStatsAccounts(context.Background(), true)
		if err != nil || len(excluded) != 1 || excluded[0] != "acc:2" {
			t.Fatal(excluded, err)
		}
	}
	state.mu.Lock()
	banQueriesBefore := 0
	for _, q := range state.queries {
		if strings.Contains(q, "web_accounts_bans") {
			banQueriesBefore++
		}
	}
	state.mu.Unlock()
	if banQueriesBefore != 1 {
		t.Fatalf("expected 1 registration ban scan across 3 cached calls, got %d", banQueriesBefore)
	}

	// Mock runtime ban endpoint and trigger POST /api/v1/anti-cheat/bans -> must invalidate cache immediately!
	rtSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(adminapi.Response{
			Version: 1,
			OK:      true,
			Ban:     &moderation.Ban{Kind: "account", Target: "acc:1"},
		})
	}))
	defer rtSrv.Close()
	rtClient, err := adminapi.NewClient(rtSrv.URL+adminapi.Path, strings.Repeat("r", 32))
	if err != nil {
		t.Fatal(err)
	}
	s.deps.Runtime = rtClient
	banReq := httptest.NewRequest("POST", "/api/v1/anti-cheat/bans", strings.NewReader(`{"kind":"account","target":"acc:1","reason":"test","requestId":"req-1234567890123456"}`))
	banReq.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	banReq.Header.Set("Content-Type", "application/json")
	banReq.Header.Set("X-CSRF-Token", s.v1CSRF(banReq))
	banRec := httptest.NewRecorder()
	s.routes().ServeHTTP(banRec, banReq)
	if banRec.Code != 200 {
		t.Fatalf("POST /api/v1/anti-cheat/bans failed (%d): %s", banRec.Code, banRec.Body.String())
	}

	if _, _, err := s.moderationStatsAccounts(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	banQueriesAfter := 0
	for _, q := range state.queries {
		if strings.Contains(q, "web_accounts_bans") {
			banQueriesAfter++
		}
	}
	state.mu.Unlock()
	if banQueriesAfter != 2 {
		t.Fatalf("expected POST /api/v1/anti-cheat/bans to invalidate moderationAccountCache (want 2 scans, got %d)", banQueriesAfter)
	}
}
