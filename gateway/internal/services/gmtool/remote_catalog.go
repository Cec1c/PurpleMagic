package gmtool

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/profession"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/skill"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	platformpvf "dnf90.local/gateway/internal/platform/pvf"
	"dnf90.local/gateway/internal/servicehttp"
)

const (
	defaultRemoteItemCacheMax  = 65536
	defaultRemoteQuestCacheMax = 65536
	remoteCatalogChunkSize     = 200
)

// RemoteCatalog carries typed definitions, never archive paths or SQL. Each
// instance is immutable and pinned to the gateway's approved full PVF hash.
type RemoteCatalog struct {
	peer             *servicehttp.Client
	generationMu     sync.Mutex
	cachedGeneration string
	mu               sync.Mutex
	maxItems         int
	items            map[uint32]catalogItem
	itemUsed         map[uint32]uint64
	itemClock        uint64
	metadataMu       sync.Mutex
	manifestHash     string
	professionMu     sync.Mutex
	professionCache  map[byte]*professionManagementCache
	professionUsed   map[byte]uint64
	professionClock  uint64
	quotaMu          sync.Mutex
	dungeonQuotas    *adminops.DungeonQuotaCatalog
	questMu          sync.Mutex
	quests           map[int64]catalogQuest
	staticMu         sync.Mutex
	staticRaw        map[string]json.RawMessage
	staticInflight   map[string]chan struct{}
}
type catalogItem struct {
	Entry              itemEntry
	SlotStart, SlotEnd int16
	Found              bool
}
type catalogQuest struct {
	Row        questIndexRow
	Definition quest.Definition
	Found      bool
}
type catalogProfession struct {
	Manifest     professionIndexManifest
	ManifestHash string
	Job          professionJobPayload
	Quests       []quest.Definition
}
type catalogRequest struct {
	Operation    string                  `json:"operation"`
	ItemIDs      []uint32                `json:"itemIds,omitempty"`
	ItemID       uint32                  `json:"itemId,omitempty"`
	QuestIDs     []int64                 `json:"questIds,omitempty"`
	Job          byte                    `json:"job,omitempty"`
	Level        int                     `json:"level,omitempty"`
	Grow         int64                   `json:"grow,omitempty"`
	CharacterJob string                  `json:"characterJob,omitempty"`
	Target       itemcfgTarget           `json:"target,omitempty"`
	Stack        repository.ItemStack    `json:"stack,omitempty"`
	Options      *itemcfgOptionsRequest  `json:"options,omitempty"`
	At           time.Time               `json:"at,omitempty"`
	Seed         *uint32                 `json:"seed,omitempty"`
	Draft        testbenefits.MailDraft  `json:"draft,omitempty"`
	Reward       v1RewardInput           `json:"reward,omitempty"`
	Scope        testbenefits.GrantScope `json:"scope,omitempty"`
}
type catalogResult struct {
	Value  json.RawMessage `json:"value,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
	Status int             `json:"status,omitempty"`
}
type catalogReward struct {
	Job  testbenefits.Job
	Name string
}

func NewRemoteCatalog(config servicehttp.Config) (*RemoteCatalog, error) {
	p, e := servicehttp.New(config)
	if e != nil {
		return nil, e
	}
	return &RemoteCatalog{
		peer:             p,
		cachedGeneration: p.Hash(),
		maxItems:         defaultRemoteItemCacheMax,
		items:            make(map[uint32]catalogItem),
		quests:           make(map[int64]catalogQuest),
		staticRaw:        make(map[string]json.RawMessage),
		staticInflight:   make(map[string]chan struct{}),
	}, nil
}
func (c *RemoteCatalog) syncGeneration() string {
	hash := c.peer.Hash()
	c.generationMu.Lock()
	prev := c.cachedGeneration
	if prev == hash {
		c.generationMu.Unlock()
		return hash
	}
	c.cachedGeneration = hash
	c.generationMu.Unlock()
	if prev == "" {
		return hash
	}
	c.mu.Lock()
	clear(c.items)
	clear(c.itemUsed)
	c.itemClock = 0
	c.mu.Unlock()

	c.metadataMu.Lock()
	c.manifestHash = ""
	c.metadataMu.Unlock()

	c.professionMu.Lock()
	clear(c.professionCache)
	clear(c.professionUsed)
	c.professionClock = 0
	c.professionMu.Unlock()

	c.quotaMu.Lock()
	c.dungeonQuotas = nil
	c.quotaMu.Unlock()

	c.questMu.Lock()
	clear(c.quests)
	c.questMu.Unlock()

	c.staticMu.Lock()
	for k := range c.staticRaw {
		if !strings.HasPrefix(k, hash+"\x00") {
			delete(c.staticRaw, k)
		}
	}
	c.staticMu.Unlock()
	return hash
}
func staticCatalogCacheKey(gen string, in catalogRequest) string {
	switch in.Operation {
	case "expert", "vault":
		return gen + "\x00" + in.Operation
	case "level":
		return fmt.Sprintf("%s\x00level:%s:%d:%d", gen, in.CharacterJob, in.Grow, in.Level)
	case "skill-points":
		return fmt.Sprintf("%s\x00skill-points:%d", gen, in.Level)
	case "cube":
		return fmt.Sprintf("%s\x00cube:%d", gen, in.ItemID)
	default:
		return ""
	}
}
func (c *RemoteCatalog) Snapshot() platformpvf.ArchiveSnapshot {
	return platformpvf.ArchiveSnapshot{Checksum: c.peer.Hash()}
}
func (c *RemoteCatalog) ReadText(string) (string, error) {
	return "", errors.New("raw PVF reads are not available on the game gateway")
}
func (c *RemoteCatalog) callRaw(ctx context.Context, in catalogRequest) (json.RawMessage, error) {
	var res catalogResult
	if e := c.peer.JSON(ctx, "/internal/catalog/v1/resolve", in, &res); e != nil {
		return nil, e
	}
	if res.Error != "" {
		if res.Code == "not_found" {
			return nil, os.ErrNotExist
		}
		if res.Status != 0 {
			return nil, &v1MailInputError{res.Status, res.Code, res.Error}
		}
		return nil, errors.New(res.Error)
	}
	return res.Value, nil
}
func (c *RemoteCatalog) call(ctx context.Context, in catalogRequest, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	gen := c.syncGeneration()
	key := staticCatalogCacheKey(gen, in)
	if key == "" {
		raw, err := c.callRaw(ctx, in)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.staticMu.Lock()
		if c.staticRaw == nil {
			c.staticRaw = make(map[string]json.RawMessage)
		}
		if c.staticInflight == nil {
			c.staticInflight = make(map[string]chan struct{})
		}
		if cached, ok := c.staticRaw[key]; ok {
			c.staticMu.Unlock()
			return json.Unmarshal(cached, out)
		}
		if wait, ok := c.staticInflight[key]; ok {
			c.staticMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wait:
				continue
			}
		}
		done := make(chan struct{})
		c.staticInflight[key] = done
		c.staticMu.Unlock()

		type rawOutcome struct {
			raw json.RawMessage
			err error
		}
		resCh := make(chan rawOutcome, 1)
		go func() {
			loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			raw, err := c.callRaw(loadCtx, in)
			c.staticMu.Lock()
			delete(c.staticInflight, key)
			if err == nil && len(raw) > 0 && c.peer.Hash() == gen {
				c.staticRaw[key] = bytes.Clone(raw)
			}
			close(done)
			c.staticMu.Unlock()
			resCh <- rawOutcome{raw: raw, err: err}
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case res := <-resCh:
			if res.err != nil {
				return res.err
			}
			return json.Unmarshal(res.raw, out)
		}
	}
}
func (c *RemoteCatalog) background(in catalogRequest, out any) error {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return c.call(ctx, in, out)
}
func (c *RemoteCatalog) item(id uint32) (itemEntry, bool, error) {
	c.syncGeneration()
	c.mu.Lock()
	v, ok := c.items[id]
	if ok {
		c.touchItemLocked(id)
	}
	c.mu.Unlock()
	if !ok {
		if e := c.background(catalogRequest{Operation: "item", ItemID: id}, &v); e != nil {
			return itemEntry{}, false, e
		}
		if v.Found && v.Entry.ID != id {
			return itemEntry{}, false, errors.New("catalog item identity mismatch")
		}
		v.Entry.SlotStart, v.Entry.SlotEnd = v.SlotStart, v.SlotEnd
		c.mu.Lock()
		c.rememberItemLocked(id, v)
		c.mu.Unlock()
	}
	return v.Entry, v.Found, nil
}

func (c *RemoteCatalog) touchItemLocked(id uint32) {
	if c.itemUsed == nil {
		c.itemUsed = map[uint32]uint64{}
	}
	c.itemClock++
	c.itemUsed[id] = c.itemClock
}
func (c *RemoteCatalog) rememberItemLocked(id uint32, v catalogItem) {
	capLimit := c.maxItems
	if capLimit <= 0 {
		capLimit = defaultRemoteItemCacheMax
	}
	if _, exists := c.items[id]; !exists && len(c.items) >= capLimit {
		var victim uint32
		oldest := ^uint64(0)
		for key := range c.items {
			if at := c.itemUsed[key]; at < oldest {
				oldest = at
				victim = key
			}
		}
		delete(c.items, victim)
		delete(c.itemUsed, victim)
	}
	c.items[id] = v
	c.touchItemLocked(id)
}

func (c *RemoteCatalog) preload(ctx context.Context, ids []uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.syncGeneration()
	missing := []uint32{}
	seen := map[uint32]bool{}
	c.mu.Lock()
	for _, id := range ids {
		if _, ok := c.items[id]; !ok && !seen[id] {
			missing = append(missing, id)
			seen[id] = true
		} else if _, ok := c.items[id]; ok {
			c.touchItemLocked(id)
		}
	}
	c.mu.Unlock()
	if len(missing) == 0 {
		return nil
	}
	for start := 0; start < len(missing); start += remoteCatalogChunkSize {
		end := start + remoteCatalogChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		chunk := missing[start:end]
		var rows []catalogItem
		if e := c.call(ctx, catalogRequest{Operation: "items", ItemIDs: chunk}, &rows); e != nil {
			return e
		}
		if len(rows) != len(chunk) {
			return errors.New("catalog item response mismatch")
		}
		for i, v := range rows {
			if v.Entry.ID != chunk[i] {
				return errors.New("catalog item identity mismatch")
			}
		}
		c.mu.Lock()
		for _, v := range rows {
			v.Entry.SlotStart, v.Entry.SlotEnd = v.SlotStart, v.SlotEnd
			c.rememberItemLocked(v.Entry.ID, v)
		}
		c.mu.Unlock()
	}
	return nil
}
func (c *RemoteCatalog) profession(ctx context.Context, job byte) (*professionDiskIndex, professionJobResources, *quest.Catalog, error) {
	c.syncGeneration()
	c.professionMu.Lock()
	defer c.professionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, professionJobResources{}, nil, err
	}
	if cached := c.professionCache[job]; cached != nil {
		c.professionClock++
		c.professionUsed[job] = c.professionClock
		return cached.index, cached.resources, cached.quests, nil
	}
	var v catalogProfession
	fail := func(e error) (*professionDiskIndex, professionJobResources, *quest.Catalog, error) {
		return nil, professionJobResources{}, nil, e
	}
	if e := c.call(ctx, catalogRequest{Operation: "profession", Job: job}, &v); e != nil {
		return fail(e)
	}
	if v.Job.Job != job || len(v.Quests) > 4096 {
		return fail(errors.New("catalog profession identity mismatch"))
	}
	profiles, e := profession.ProfilesFromText(job, v.Job.Profile)
	if e != nil {
		return fail(e)
	}
	skills, e := skill.TableFromDefinitions(job, v.Job.Skills)
	if e != nil {
		return fail(e)
	}
	quests, e := quest.CatalogFromDefinitions(v.Quests)
	if e != nil {
		return fail(e)
	}
	cached := &professionManagementCache{index: &professionDiskIndex{Checksum: c.peer.Hash(), ManifestHash: v.ManifestHash, Manifest: v.Manifest}, resources: professionJobResources{Profiles: profiles, Skills: skills}, quests: quests, job: job, loaded: true}
	if c.professionCache == nil {
		c.professionCache = map[byte]*professionManagementCache{}
		c.professionUsed = map[byte]uint64{}
	}
	if len(c.professionCache) >= 32 {
		var victim byte
		oldest := ^uint64(0)
		for k, at := range c.professionUsed {
			if at < oldest {
				oldest = at
				victim = k
			}
		}
		delete(c.professionCache, victim)
		delete(c.professionUsed, victim)
	}
	c.professionClock++
	c.professionUsed[job] = c.professionClock
	c.professionCache[job] = cached
	return cached.index, cached.resources, cached.quests, nil
}

// The catalog client and its authenticated peer are pinned to one full PVF hash.
// Static metadata is immutable for this generation; failures are never cached.
func (c *RemoteCatalog) metadata(ctx context.Context) (string, error) {
	c.syncGeneration()
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.manifestHash != "" {
		return c.manifestHash, nil
	}
	var v struct{ ManifestHash string }
	if err := c.call(ctx, catalogRequest{Operation: "metadata"}, &v); err != nil {
		return "", err
	}
	if v.ManifestHash == "" {
		return "", errors.New("empty catalog manifest")
	}
	c.manifestHash = v.ManifestHash
	return c.manifestHash, nil
}
func (c *RemoteCatalog) dungeonQuotaCatalog(ctx context.Context) (adminops.DungeonQuotaCatalog, error) {
	c.syncGeneration()
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	if err := ctx.Err(); err != nil {
		return adminops.DungeonQuotaCatalog{}, err
	}
	if c.dungeonQuotas != nil {
		return cloneDungeonQuotaCatalog(*c.dungeonQuotas), nil
	}
	var v adminops.DungeonQuotaCatalog
	if err := c.call(ctx, catalogRequest{Operation: "dungeon-quotas"}, &v); err != nil {
		return adminops.DungeonQuotaCatalog{}, err
	}
	if v.PVFChecksum != c.peer.Hash() || v.Validate() != nil {
		return adminops.DungeonQuotaCatalog{}, adminops.ErrDungeonQuotaDefinition
	}
	cached := cloneDungeonQuotaCatalog(v)
	c.dungeonQuotas = &cached
	return cloneDungeonQuotaCatalog(v), nil
}
func (c *RemoteCatalog) questIndex(ctx context.Context) (*questDiskIndex, error) {
	hash, err := c.metadata(ctx)
	if err != nil {
		return nil, err
	}
	return &questDiskIndex{Checksum: c.peer.Hash(), ManifestHash: hash, remote: c, remoteRows: make(map[int64]catalogQuest)}, nil
}
func (ix *questDiskIndex) remoteLoad(ctx context.Context, ids []int64) error {
	if ix.remote == nil || len(ids) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ix.remote.syncGeneration()
	missing := []int64{}
	seen := map[int64]bool{}
	ix.remote.questMu.Lock()
	if ix.remote.quests == nil {
		ix.remote.quests = make(map[int64]catalogQuest)
	}
	for _, id := range ids {
		if v, ok := ix.remote.quests[id]; ok {
			ix.remoteRows[id] = v
		} else if !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	ix.remote.questMu.Unlock()
	for start := 0; start < len(missing); start += remoteCatalogChunkSize {
		end := start + remoteCatalogChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		chunk := missing[start:end]
		var rows []catalogQuest
		if e := ix.remote.call(ctx, catalogRequest{Operation: "quests", QuestIDs: chunk}, &rows); e != nil {
			return e
		}
		if len(rows) != len(chunk) {
			return errors.New("catalog quest response mismatch")
		}
		for i, v := range rows {
			if v.Row.ID != chunk[i] || (v.Found && v.Definition.ID != chunk[i]) {
				return errors.New("catalog quest identity mismatch")
			}
			ix.remoteRows[chunk[i]] = v
		}
		ix.remote.questMu.Lock()
		if len(ix.remote.quests)+len(rows) <= defaultRemoteQuestCacheMax {
			for i, v := range rows {
				ix.remote.quests[chunk[i]] = v
			}
		}
		ix.remote.questMu.Unlock()
	}
	return nil
}

// NewCatalog constructs only static resources and administrator sessions. It
// has no database pool, repository provider, or game-runtime client.
func NewCatalog(ctx context.Context, cfg Config, source dnfpvf.Source) (*Server, error) {
	if len(cfg.Password) < 32 || source == nil || itemIndexSourceChecksum(source) == "" {
		return nil, errors.New("invalid external GM catalog configuration")
	}
	if cfg.Logger == nil {
		cfg.Logger = func(string, ...any) {}
	}
	s := &Server{cfg: cfg, deps: Deps{PVF: func() (dnfpvf.Source, error) { return source, nil }}, sessions: make(map[string]time.Time), failures: make(map[string][]time.Time), grants: make(map[string]grantRecord), pvf: newPVFCache()}
	if err := s.loadRememberedSessions(); err != nil {
		return nil, err
	}
	s.items = newItemIndex(cfg.ItemIndexCachePath, s, cfg.Logger)
	if e := s.items.loadCache(); e != nil {
		return nil, e
	}
	if _, e := s.questReadIndex(ctx); e != nil {
		return nil, e
	}
	return s, nil
}

func (s *Server) CatalogHandler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/catalog/v1/resolve", func(w http.ResponseWriter, r *http.Request) {
		var in catalogRequest
		if !v1DecodeLimit(w, r, &in, 256<<10) {
			return
		}
		ctx, stop := context.WithTimeout(r.Context(), 8*time.Second)
		defer stop()
		value, e := s.resolveCatalog(ctx, in)
		out := catalogResult{}
		if e != nil {
			out.Error = e.Error()
			var input *v1MailInputError
			if errors.As(e, &input) {
				out.Status = input.status
				out.Code = input.code
			}
			if errors.Is(e, os.ErrNotExist) {
				out.Code = "not_found"
			}
		} else {
			out.Value, e = json.Marshal(value)
			if e != nil {
				out.Error = "definition encoding failed"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Vary", "Accept-Encoding")
		// Go service clients negotiate gzip automatically. Profession definitions
		// contain repeated JSON keys and cross the private WAN link on cold reads.
		if r.Header.Get("Accept-Encoding") == "gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			compressed := gzip.NewWriter(w)
			defer compressed.Close()
			json.NewEncoder(compressed).Encode(out)
			return
		}
		json.NewEncoder(w).Encode(out)
	})
	return servicehttp.Protect(token, s.v1PVFChecksum(), mux)
}
func (s *Server) resolveCatalog(ctx context.Context, in catalogRequest) (any, error) {
	switch in.Operation {
	case "metadata":
		ix, e := s.questReadIndex(ctx)
		if e != nil {
			return nil, e
		}
		return struct{ ManifestHash string }{ix.ManifestHash}, nil
	case "items":
		if len(in.ItemIDs) > 200 {
			return nil, errors.New("item lookup limit exceeded")
		}
		out := make([]catalogItem, 0, len(in.ItemIDs))
		for _, id := range in.ItemIDs {
			v, ok := s.items.lookup(id)
			if !ok {
				v.ID = id
			}
			out = append(out, catalogItem{v, v.SlotStart, v.SlotEnd, ok})
		}
		return out, nil
	case "cube":
		return s.accountCubeDefinition(int64(in.ItemID))
	case "item":
		e, ok := s.items.lookup(in.ItemID)
		return catalogItem{e, e.SlotStart, e.SlotEnd, ok}, nil
	case "quests":
		if len(in.QuestIDs) < 1 || len(in.QuestIDs) > 200 {
			return nil, errors.New("quest lookup requires 1..200 IDs")
		}
		ix, e := s.questReadIndex(ctx)
		if e != nil {
			return nil, e
		}
		out := make([]catalogQuest, 0, len(in.QuestIDs))
		for _, id := range in.QuestIDs {
			if id <= 0 || id > 2147483647 {
				return nil, errors.New("invalid quest ID")
			}
			row, ok := ix.find(id)
			v := catalogQuest{Row: row, Found: ok}
			v.Row.ID = id
			if ok {
				v.Definition, e = ix.definition(ctx, id)
				if e != nil {
					return nil, e
				}
			}
			out = append(out, v)
		}
		return out, nil
	case "profession":
		ix, e := readProfessionIndex(ctx, professionIndexPath(s.items.cachePath, s.v1PVFChecksum()), s.v1PVFChecksum())
		if e != nil {
			return nil, e
		}
		v := catalogProfession{Manifest: ix.Manifest, ManifestHash: ix.ManifestHash}
		found := false
		for _, j := range ix.Manifest.Jobs {
			if j.Job == in.Job {
				e = ix.record(ctx, j.Record, &v.Job)
				found = true
				break
			}
		}
		if !found {
			return nil, profession.ErrProfileMissing
		}
		if e != nil {
			return nil, e
		}
		e = ix.record(ctx, ix.Manifest.Quests, &v.Quests)
		return v, e
	case "expert":
		return s.expertJobDefinitions(ctx)
	case "dungeon-quotas":
		return s.dungeonQuotaCatalog(ctx)
	case "level":
		return s.levelDefinition(ctx, repository.CharacterRecord{Job: in.CharacterJob, Stats: map[string]int64{"grow_type": in.Grow}}, in.Level)
	case "skill-points":
		return s.skillPointDefinition(ctx, in.Level)
	case "vault":
		return s.vaultDefinition()
	case "reward":
		j, n, e := s.v1ResolveReward(in.Reward, in.Scope)
		return catalogReward{j, n}, e
	case "mail":
		return s.v1ProjectMailDraft(in.Draft, in.At)
	case "campaign":
		j, n, e := s.v1ProjectCampaignPlan(in.Draft)
		return catalogReward{j, n}, e
	case "item-capability", "item-apply":
		entry, ok := s.items.lookup(in.ItemID)
		if !ok || entry.Invalid || in.Stack.ItemID != int64(in.ItemID) {
			return nil, errors.New("catalog item identity mismatch")
		}
		if in.Operation == "item-capability" {
			return s.itemcfgBuildCapability(entry, in.Stack, in.At)
		}
		return s.itemcfgApplyOptionsWithSeed(in.Target, entry, in.Stack, in.Options, in.At, in.Seed)
	}
	return nil, errors.New("unknown catalog operation")
}

func (s *Server) remoteLevel(ctx context.Context, c repository.CharacterRecord, level int) (adminops.LevelDefinition, error) {
	var d adminops.LevelDefinition
	e := s.deps.Catalog.call(ctx, catalogRequest{Operation: "level", Level: level, CharacterJob: c.Job, Grow: c.Stats["grow_type"]}, &d)
	return d, e
}
