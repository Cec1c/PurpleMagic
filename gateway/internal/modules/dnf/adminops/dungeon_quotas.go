package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/premium"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/raid"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const (
	pandemoniumRiftDungeonID int64 = 316
	pandemoniumRiftDailyMax  int   = 3
	echonWorldMapIndex       int64 = 45

	dungeonCyclePVFPath = "etc/dungeoncyclesystem.etc"
	antonRaidPVFPath    = "etc/raid/anton.etc"
	lukeRaidPVFPath     = "etc/raid/luke/luke.etc"
	fiendWarPVFPath     = "etc/raid/fiendwar/fiendwar.etc"
)

var (
	ErrDungeonQuotaInput      = errors.New("invalid dungeon quota operation")
	ErrDungeonQuotaState      = errors.New("invalid stored dungeon quota state")
	ErrDungeonQuotaZero       = errors.New("dungeon quota has no used count to reset")
	ErrDungeonQuotaDefinition = errors.New("dungeon quota PVF definition invalid or changed")
)

type DungeonQuotaCatalog struct {
	PVFChecksum      string  `json:"pvfChecksum"`
	Version          int     `json:"version"`
	EchonTempleIDs   []int64 `json:"echonTempleIds"`
	EchonTempleLimit int     `json:"echonTempleLimit"`
	EchonTrialIDs    []int64 `json:"echonTrialIds"`
	EchonTrialLimit  int     `json:"echonTrialLimit"`
	AntonWeeklyLimit int     `json:"antonWeeklyLimit"`
	LukeWeeklyLimit  int     `json:"lukeWeeklyLimit"`
	FiendWeeklyLimit int     `json:"fiendWeeklyLimit"`
}

func (d DungeonQuotaCatalog) Validate() error {
	raw, err := hex.DecodeString(d.PVFChecksum)
	if err != nil || len(raw) != 32 || d.Version != 1 {
		return ErrDungeonQuotaDefinition
	}
	if len(d.EchonTempleIDs) == 0 || len(d.EchonTempleIDs) > 16 || len(d.EchonTrialIDs) == 0 || len(d.EchonTrialIDs) > 16 {
		return ErrDungeonQuotaDefinition
	}
	for _, limit := range []int{d.EchonTempleLimit, d.EchonTrialLimit, d.AntonWeeklyLimit, d.LukeWeeklyLimit, d.FiendWeeklyLimit} {
		if limit <= 0 || limit > 255 {
			return ErrDungeonQuotaDefinition
		}
	}
	seen := map[int64]bool{pandemoniumRiftDungeonID: true}
	for _, group := range [][]int64{d.EchonTempleIDs, d.EchonTrialIDs} {
		var prev int64
		for _, id := range group {
			if id <= 0 || id <= prev || seen[id] {
				return ErrDungeonQuotaDefinition
			}
			prev = id
			seen[id] = true
		}
	}
	return nil
}

type DungeonQuotaResolver func(context.Context) (DungeonQuotaCatalog, error)

type DungeonQuotaEntry struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	ResetSchedule string `json:"resetSchedule"`
	Used          int    `json:"used"`
	Limit         int    `json:"limit"`
	CanReset      bool   `json:"canReset"`
	Detail        string `json:"detail"`
	TempleEntries *int   `json:"templeEntries,omitempty"`
	TempleClears  *int   `json:"templeClears,omitempty"`
	TempleLimit   *int   `json:"templeLimit,omitempty"`
	TrialEntries  *int   `json:"trialEntries,omitempty"`
	TrialClears   *int   `json:"trialClears,omitempty"`
	TrialLimit    *int   `json:"trialLimit,omitempty"`
}

type DungeonQuotaCommand struct {
	AccountID          string `json:"accountId"`
	CharacterID        string `json:"characterId"`
	Action             string `json:"action"`
	QuotaKey           string `json:"quotaKey"`
	Revision           string `json:"revision"`
	PVFChecksum        string `json:"pvfChecksum"`
	DefinitionRevision string `json:"definitionRevision"`
	Reason             string `json:"reason"`
	Actor              string `json:"actor"`
	PlanToken          string `json:"planToken,omitempty"`
	Key                string `json:"-"`
}

type DungeonQuotaReceipt struct {
	RequestID          string              `json:"requestId,omitempty"`
	Kind               string              `json:"kind"`
	AccountID          string              `json:"accountId"`
	CharacterID        string              `json:"characterId"`
	Action             string              `json:"action"`
	QuotaKey           string              `json:"quotaKey"`
	QuotaName          string              `json:"quotaName"`
	Before             DungeonQuotaEntry   `json:"before"`
	After              DungeonQuotaEntry   `json:"after"`
	Quotas             []DungeonQuotaEntry `json:"quotas"`
	PVFChecksum        string              `json:"pvfChecksum"`
	DefinitionRevision string              `json:"definitionRevision"`
	Revision           string              `json:"revision"`
	Reason             string              `json:"reason"`
	Actor              string              `json:"actor"`
	CreatedAt          time.Time           `json:"createdAt"`
}

type dungeonQuotaStored struct {
	Receipt     DungeonQuotaReceipt `json:"receipt"`
	ContentHash string              `json:"contentHash"`
}

func dungeonQuotaHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func DungeonQuotaIdentity(c DungeonQuotaCommand) string               { return dungeonQuotaHash(c) }
func DungeonQuotaDefinitionRevision(d DungeonQuotaCatalog) string     { return dungeonQuotaHash(d) }
func DefaultDungeonQuotaWeeklyPolicy() (raid.WeeklyPolicy, error) {
	policy := raid.WeeklyPolicy{ResetWeekday: time.Thursday, ResetHour: 6}
	for _, field := range []struct {
		name string
		max  int
		set  func(int)
	}{
		{"DNFBRIDGE_RAID_WEEKLY_RESET_DAY", 6, func(v int) { policy.ResetWeekday = time.Weekday(v) }},
		{"DNFBRIDGE_RAID_WEEKLY_RESET_HOUR", 23, func(v int) { policy.ResetHour = v }},
	} {
		raw := strings.TrimSpace(os.Getenv(field.name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > field.max {
			return raid.WeeklyPolicy{}, ErrDungeonQuotaState
		}
		field.set(value)
	}
	return policy, nil
}

func LoadDungeonQuotaCatalog(source dnfpvf.Source, checksum string) (DungeonQuotaCatalog, error) {
	if source == nil {
		return DungeonQuotaCatalog{}, ErrDungeonQuotaDefinition
	}
	cycleText, err := source.ReadText(dungeonCyclePVFPath)
	if err != nil {
		return DungeonQuotaCatalog{}, err
	}
	templeIDs, templeLimit, trialIDs, trialLimit, err := parseEchonCycleCatalog(cycleText)
	if err != nil {
		return DungeonQuotaCatalog{}, err
	}
	antonCfg, err := raid.LoadBattleConfiguration(source, antonRaidPVFPath)
	if err != nil {
		return DungeonQuotaCatalog{}, err
	}
	lukeCfg, err := raid.LoadBattleConfiguration(source, lukeRaidPVFPath)
	if err != nil {
		return DungeonQuotaCatalog{}, err
	}
	fiendWeekly, err := loadFiendWarWeeklyClearLimit(source)
	if err != nil {
		return DungeonQuotaCatalog{}, err
	}
	out := DungeonQuotaCatalog{
		PVFChecksum:      checksum,
		Version:          1,
		EchonTempleIDs:   templeIDs,
		EchonTempleLimit: templeLimit,
		EchonTrialIDs:    trialIDs,
		EchonTrialLimit:  trialLimit,
		AntonWeeklyLimit: antonCfg.WeeklyClearLimit,
		LukeWeeklyLimit:  lukeCfg.WeeklyClearLimit,
		FiendWeeklyLimit: fiendWeekly,
	}
	if err := out.Validate(); err != nil {
		return DungeonQuotaCatalog{}, err
	}
	return out, nil
}

func parseEchonCycleCatalog(text string) ([]int64, int, []int64, int, error) {
	doc, err := dnfpvf.Parse(dungeonCyclePVFPath, text)
	if err != nil {
		return nil, 0, nil, 0, ErrDungeonQuotaDefinition
	}
	values := doc.Ints("dungeon cycle")
	if len(values) == 0 || len(values)%5 != 0 {
		return nil, 0, nil, 0, ErrDungeonQuotaDefinition
	}
	seen := make(map[int64]bool, len(values)/5)
	var templeIDs, trialIDs []int64
	var templeLimit, trialLimit int
	for row := 0; row < len(values); row += 5 {
		worldMap := values[row]
		dungeonID := values[row+2]
		dailyCount := values[row+3]
		order := values[row+4]
		if dungeonID <= 0 || dailyCount <= 0 || dailyCount > 255 || seen[dungeonID] {
			return nil, 0, nil, 0, ErrDungeonQuotaDefinition
		}
		seen[dungeonID] = true
		if worldMap != echonWorldMapIndex {
			continue
		}
		switch order {
		case 0:
			if templeLimit == 0 {
				templeLimit = int(dailyCount)
			} else if templeLimit != int(dailyCount) {
				return nil, 0, nil, 0, ErrDungeonQuotaDefinition
			}
			templeIDs = append(templeIDs, dungeonID)
		case 1:
			if trialLimit == 0 {
				trialLimit = int(dailyCount)
			} else if trialLimit != int(dailyCount) {
				return nil, 0, nil, 0, ErrDungeonQuotaDefinition
			}
			trialIDs = append(trialIDs, dungeonID)
		default:
			return nil, 0, nil, 0, ErrDungeonQuotaDefinition
		}
	}
	sort.Slice(templeIDs, func(i, j int) bool { return templeIDs[i] < templeIDs[j] })
	sort.Slice(trialIDs, func(i, j int) bool { return trialIDs[i] < trialIDs[j] })
	if len(templeIDs) == 0 || len(trialIDs) == 0 || templeLimit <= 0 || trialLimit <= 0 {
		return nil, 0, nil, 0, ErrDungeonQuotaDefinition
	}
	return templeIDs, templeLimit, trialIDs, trialLimit, nil
}

func loadFiendWarWeeklyClearLimit(source dnfpvf.Source) (int, error) {
	text, err := source.ReadText(fiendWarPVFPath)
	if err != nil {
		return 0, err
	}
	doc, err := dnfpvf.Parse(fiendWarPVFPath, text)
	if err != nil {
		return 0, ErrDungeonQuotaDefinition
	}
	end := len(doc.Sections)
	foundPhase := false
	for i, section := range doc.Sections {
		if strings.EqualFold(strings.TrimSpace(section.Name), "PHASE") {
			end = i
			foundPhase = true
			break
		}
	}
	if !foundPhase {
		return 0, ErrDungeonQuotaDefinition
	}
	weekly := 0
	seen := false
	for i := 0; i < end; i++ {
		section := doc.Sections[i]
		if !strings.EqualFold(strings.TrimSpace(section.Name), "WEEKLY CLEAR COUNT") {
			continue
		}
		tokens := doc.Tokens[section.Start:section.End]
		if seen || len(tokens) != 1 || tokens[0].Kind != dnfpvf.TokenInt || tokens[0].Int <= 0 || tokens[0].Int > 255 {
			return 0, ErrDungeonQuotaDefinition
		}
		seen = true
		weekly = int(tokens[0].Int)
	}
	if !seen {
		return 0, ErrDungeonQuotaDefinition
	}
	return weekly, nil
}

func dailyStatKeys(prefix string, dungeonID int64) []string {
	base := fmt.Sprintf("%s_%d_", prefix, dungeonID)
	return []string{base + "day", base + "used", base + "nonce_hi", base + "nonce_lo"}
}

func DungeonQuotaDeleteKeys(quotaKey string, d DungeonQuotaCatalog) ([]string, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	switch quotaKey {
	case "pandemonium_rift":
		return dailyStatKeys("dungeon_daily_entry", pandemoniumRiftDungeonID), nil
	case "echon":
		ids := make([]int64, 0, len(d.EchonTempleIDs)+len(d.EchonTrialIDs))
		ids = append(ids, d.EchonTempleIDs...)
		ids = append(ids, d.EchonTrialIDs...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		keys := make([]string, 0, len(ids)*8)
		for _, id := range ids {
			keys = append(keys, dailyStatKeys("dungeon_daily_entry", id)...)
			keys = append(keys, dailyStatKeys("dungeon_cycle_entry", id)...)
		}
		return keys, nil
	case "anton_raid":
		return []string{"raid_weekly_epoch_v1_1", "raid_weekly_clears_v1_1"}, nil
	case "luke_raid":
		return []string{"raid_weekly_epoch_v1_2", "raid_weekly_clears_v1_2"}, nil
	case "fiend_war":
		return []string{"raid_weekly_epoch_v1_3", "raid_weekly_clears_v1_3"}, nil
	default:
		return nil, ErrDungeonQuotaInput
	}
}

func allManagedQuotaStatKeys(d DungeonQuotaCatalog) []string {
	var all []string
	for _, qk := range []string{"pandemonium_rift", "echon", "anton_raid", "luke_raid", "fiend_war"} {
		keys, _ := DungeonQuotaDeleteKeys(qk, d)
		all = append(all, keys...)
	}
	sort.Strings(all)
	return all
}

func DungeonQuotaRevision(v repository.DungeonQuotaManagementState, d DungeonQuotaCatalog, now time.Time, policy raid.WeeklyPolicy) string {
	epoch, _ := raid.WeeklyEpoch(now, policy)
	type kv struct {
		Key   string `json:"k"`
		Has   bool   `json:"h"`
		Value int64  `json:"v"`
	}
	keys := allManagedQuotaStatKeys(d)
	pairs := make([]kv, 0, len(keys))
	for _, k := range keys {
		val, ok := v.Character.Stats[k]
		pairs = append(pairs, kv{Key: k, Has: ok, Value: val})
	}
	c := v.Character
	return dungeonQuotaHash([]any{
		c.CharacterID,
		c.AccountID,
		c.Stats["delete_flag"],
		c.Stats["gm_dungeon_quota_sequence_v1"],
		c.UpdatedAt.UTC(),
		premium.ServiceDayID(now),
		epoch,
		pairs,
	})
}

func dailyCounterUsed(stats map[string]int64, prefix string, dungeonID int64, serviceDay int64) (int, error) {
	base := fmt.Sprintf("%s_%d_", prefix, dungeonID)
	if stats[base+"day"] != serviceDay {
		return 0, nil
	}
	used := stats[base+"used"]
	if used < 0 || used > 10000 {
		return 0, ErrDungeonQuotaState
	}
	return int(used), nil
}

func echonGroupCounts(stats map[string]int64, ids []int64, serviceDay int64) (entries int, clears int, err error) {
	for _, id := range ids {
		clearUsed, e := dailyCounterUsed(stats, "dungeon_daily_entry", id, serviceDay)
		if e != nil {
			return 0, 0, e
		}
		entryUsed, e := dailyCounterUsed(stats, "dungeon_cycle_entry", id, serviceDay)
		if e != nil {
			return 0, 0, e
		}
		if clearUsed > entryUsed {
			entryUsed = clearUsed
		}
		entries += entryUsed
		clears += clearUsed
	}
	return entries, clears, nil
}

func intPtr(v int) *int { return &v }

func DungeonQuotaValues(v repository.DungeonQuotaManagementState, d DungeonQuotaCatalog, now time.Time, policy raid.WeeklyPolicy) ([]DungeonQuotaEntry, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if now.IsZero() || v.Character.Stats["gm_dungeon_quota_sequence_v1"] < 0 {
		return nil, ErrDungeonQuotaState
	}
	serviceDay := premium.ServiceDayID(now)
	riftUsed, err := dailyCounterUsed(v.Character.Stats, "dungeon_daily_entry", pandemoniumRiftDungeonID, serviceDay)
	if err != nil {
		return nil, err
	}
	riftDetail := "今日入场次数已用尽"
	if riftUsed < pandemoniumRiftDailyMax {
		riftDetail = fmt.Sprintf("下次入场消耗 %d 个精炼的时空石", 2*(riftUsed+1))
	}

	templeEntries, templeClears, err := echonGroupCounts(v.Character.Stats, d.EchonTempleIDs, serviceDay)
	if err != nil {
		return nil, err
	}
	trialEntries, trialClears, err := echonGroupCounts(v.Character.Stats, d.EchonTrialIDs, serviceDay)
	if err != nil {
		return nil, err
	}
	echonUsed := templeEntries + trialEntries
	echonLimit := d.EchonTempleLimit + d.EchonTrialLimit
	echonDetail := fmt.Sprintf("遗落的神殿 %d/%d（已通关 %d/%d） · 试炼 %d/%d", templeEntries, d.EchonTempleLimit, templeClears, d.EchonTempleLimit, trialEntries, d.EchonTrialLimit)

	antonUsed, err := raid.WeeklyClears(v.Character, 1, now, policy)
	if err != nil {
		return nil, ErrDungeonQuotaState
	}
	lukeUsed, err := raid.WeeklyClears(v.Character, 2, now, policy)
	if err != nil {
		return nil, ErrDungeonQuotaState
	}
	fiendUsed, err := raid.WeeklyClears(v.Character, 3, now, policy)
	if err != nil {
		return nil, ErrDungeonQuotaState
	}

	return []DungeonQuotaEntry{
		{
			Key:           "pandemonium_rift",
			Name:          "魔界裂缝",
			Category:      "daily",
			ResetSchedule: "每日 06:00",
			Used:          riftUsed,
			Limit:         pandemoniumRiftDailyMax,
			CanReset:      riftUsed > 0,
			Detail:        riftDetail,
		},
		{
			Key:           "echon",
			Name:          "艾肯（遗落的神殿 + 试炼）",
			Category:      "daily",
			ResetSchedule: "每日 06:00",
			Used:          echonUsed,
			Limit:         echonLimit,
			CanReset:      echonUsed > 0,
			Detail:        echonDetail,
			TempleEntries: intPtr(templeEntries),
			TempleClears:  intPtr(templeClears),
			TempleLimit:   intPtr(d.EchonTempleLimit),
			TrialEntries:  intPtr(trialEntries),
			TrialClears:   intPtr(trialClears),
			TrialLimit:    intPtr(d.EchonTrialLimit),
		},
		{
			Key:           "anton_raid",
			Name:          "安徒恩攻坚战",
			Category:      "weekly",
			ResetSchedule: "每周四 06:00",
			Used:          antonUsed,
			Limit:         d.AntonWeeklyLimit,
			CanReset:      antonUsed > 0,
			Detail:        "团队与单人安徒恩共用角色每周攻坚次数",
		},
		{
			Key:           "luke_raid",
			Name:          "卢克攻坚战",
			Category:      "weekly",
			ResetSchedule: "每周四 06:00",
			Used:          lukeUsed,
			Limit:         d.LukeWeeklyLimit,
			CanReset:      lukeUsed > 0,
			Detail:        "团队与单人卢克共用角色每周攻坚次数",
		},
		{
			Key:           "fiend_war",
			Name:          "超时空之战",
			Category:      "weekly",
			ResetSchedule: "每周四 06:00",
			Used:          fiendUsed,
			Limit:         d.FiendWeeklyLimit,
			CanReset:      fiendUsed > 0,
			Detail:        "角色每周攻坚通关次数",
		},
	}, nil
}

func validDungeonQuotaCommand(c DungeonQuotaCommand, commit bool) error {
	id, err := strconv.ParseUint(c.CharacterID, 10, 16)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != c.CharacterID || c.AccountID == "" || c.Actor == "" || c.Action != "reset" || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 500 {
		return ErrDungeonQuotaInput
	}
	switch c.QuotaKey {
	case "pandemonium_rift", "echon", "anton_raid", "luke_raid", "fiend_war":
	default:
		return ErrDungeonQuotaInput
	}
	for _, h := range []string{c.Revision, c.PVFChecksum} {
		raw, e := hex.DecodeString(h)
		if e != nil || len(raw) != 32 {
			return ErrDungeonQuotaInput
		}
	}
	if c.DefinitionRevision != "" {
		raw, e := hex.DecodeString(c.DefinitionRevision)
		if e != nil || len(raw) != 32 {
			return ErrDungeonQuotaInput
		}
	}
	if commit && (len(c.Key) < 16 || len(c.Key) > 128 || c.DefinitionRevision == "") {
		return ErrDungeonQuotaInput
	}
	return nil
}

func dungeonQuotaOwner(v repository.DungeonQuotaManagementState, c DungeonQuotaCommand) error {
	if v.Character.CharacterID != c.CharacterID || v.Character.AccountID != c.AccountID || v.Character.Stats["delete_flag"] != 0 {
		return ErrAccount
	}
	return nil
}

func dungeonQuotaReceipt(ctx context.Context, r repository.ManagementReceiptStore, c DungeonQuotaCommand) (DungeonQuotaReceipt, bool, error) {
	raw, found, err := r.Load(ctx, c.Key)
	if err != nil || !found {
		return DungeonQuotaReceipt{}, found, err
	}
	var old dungeonQuotaStored
	if err = json.Unmarshal(raw, &old); err != nil {
		return DungeonQuotaReceipt{}, true, err
	}
	if old.ContentHash != DungeonQuotaIdentity(c) {
		return DungeonQuotaReceipt{}, true, ErrIdempotency
	}
	return old.Receipt, true, nil
}

func findDungeonQuotaEntry(entries []DungeonQuotaEntry, key string) (DungeonQuotaEntry, bool) {
	for _, entry := range entries {
		if entry.Key == key {
			return entry, true
		}
	}
	return DungeonQuotaEntry{}, false
}

func LookupDungeonQuotaOperation(ctx context.Context, u repository.DungeonQuotaManagementUnitOfWork, c DungeonQuotaCommand) (DungeonQuotaReceipt, bool, error) {
	var result DungeonQuotaReceipt
	var found bool
	if err := validDungeonQuotaCommand(c, true); err != nil {
		return result, false, err
	}
	if u == nil {
		return result, false, ErrRuntimeUnavailable
	}
	err := u.WithinDungeonQuotaManagement(ctx, c.AccountID, c.CharacterID, func(r repository.DungeonQuotaManagementRepository, receipts repository.ManagementReceiptStore) error {
		v, err := r.LoadDungeonQuotaManagement(ctx, c.CharacterID)
		if err != nil {
			return err
		}
		if err = dungeonQuotaOwner(v, c); err != nil {
			return err
		}
		result, found, err = dungeonQuotaReceipt(ctx, receipts, c)
		return err
	})
	return result, found, err
}

func RunDungeonQuotaOperation(
	ctx context.Context,
	u repository.DungeonQuotaManagementUnitOfWork,
	c DungeonQuotaCommand,
	commit bool,
	now time.Time,
	policy raid.WeeklyPolicy,
	resolve DungeonQuotaResolver,
	offline QuestOfflineCheck,
) (DungeonQuotaReceipt, error) {
	var result DungeonQuotaReceipt
	if err := validDungeonQuotaCommand(c, commit); err != nil {
		return result, err
	}
	if u == nil {
		return result, ErrRuntimeUnavailable
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	err := u.WithinDungeonQuotaManagement(ctx, c.AccountID, c.CharacterID, func(r repository.DungeonQuotaManagementRepository, receipts repository.ManagementReceiptStore) error {
		v, err := r.LoadDungeonQuotaManagement(ctx, c.CharacterID)
		if err != nil {
			return err
		}
		if err = dungeonQuotaOwner(v, c); err != nil {
			return err
		}
		if commit {
			old, found, e := dungeonQuotaReceipt(ctx, receipts, c)
			if e != nil {
				return e
			}
			if found {
				result = old
				return nil
			}
		}
		if resolve == nil {
			return ErrDungeonQuotaDefinition
		}
		d, err := resolve(ctx)
		if err != nil {
			return err
		}
		if d.Validate() != nil || d.PVFChecksum != c.PVFChecksum {
			return ErrDungeonQuotaDefinition
		}
		defRev := DungeonQuotaDefinitionRevision(d)
		if c.DefinitionRevision != "" && c.DefinitionRevision != defRev {
			return ErrDungeonQuotaDefinition
		}
		if DungeonQuotaRevision(v, d, now, policy) != c.Revision {
			return ErrConflict
		}
		if v.Character.Stats["gm_dungeon_quota_sequence_v1"] == math.MaxInt64 {
			return ErrDungeonQuotaState
		}
		beforeList, err := DungeonQuotaValues(v, d, now, policy)
		if err != nil {
			return err
		}
		beforeEntry, ok := findDungeonQuotaEntry(beforeList, c.QuotaKey)
		if !ok {
			return ErrDungeonQuotaInput
		}
		if beforeEntry.Used <= 0 || !beforeEntry.CanReset {
			return ErrDungeonQuotaZero
		}
		deleteKeys, err := DungeonQuotaDeleteKeys(c.QuotaKey, d)
		if err != nil {
			return err
		}
		at := now.UTC().Truncate(time.Microsecond)
		if !at.After(v.Character.UpdatedAt) {
			at = v.Character.UpdatedAt.Add(time.Microsecond)
		}
		sim := v
		sim.Character = repository.CloneCharacter(v.Character)
		for _, k := range deleteKeys {
			delete(sim.Character.Stats, k)
		}
		nextSeq := v.Character.Stats["gm_dungeon_quota_sequence_v1"] + 1
		sim.Character.Stats["gm_dungeon_quota_sequence_v1"] = nextSeq
		sim.Character.UpdatedAt = at
		afterList, err := DungeonQuotaValues(sim, d, now, policy)
		if err != nil {
			return err
		}
		afterEntry, ok := findDungeonQuotaEntry(afterList, c.QuotaKey)
		if !ok || afterEntry.Used != 0 || afterEntry.CanReset {
			return ErrDungeonQuotaState
		}
		result = DungeonQuotaReceipt{
			Kind:               "dungeon-quota-operation",
			AccountID:          c.AccountID,
			CharacterID:        c.CharacterID,
			Action:             c.Action,
			QuotaKey:           c.QuotaKey,
			QuotaName:          beforeEntry.Name,
			Before:             beforeEntry,
			After:              afterEntry,
			Quotas:             afterList,
			PVFChecksum:        d.PVFChecksum,
			DefinitionRevision: defRev,
			Revision:           c.Revision,
			Reason:             c.Reason,
			Actor:              c.Actor,
			CreatedAt:          at,
		}
		if !commit {
			return nil
		}
		if offline == nil {
			return ErrRuntimeUnavailable
		}
		if err = offline(ctx, c.CharacterID); err != nil {
			return err
		}
		if err = r.ResetDungeonQuotaKeys(ctx, c.CharacterID, deleteKeys, nextSeq, at); err != nil {
			return err
		}
		if err = offline(ctx, c.CharacterID); err != nil {
			return err
		}
		persisted, err := r.LoadDungeonQuotaManagement(ctx, c.CharacterID)
		if err != nil {
			return err
		}
		actualList, err := DungeonQuotaValues(persisted, d, now, policy)
		if err != nil {
			return err
		}
		if dungeonQuotaHash(actualList) != dungeonQuotaHash(afterList) {
			return ErrConflict
		}
		result.RequestID = c.Key
		result.Quotas = actualList
		result.Revision = DungeonQuotaRevision(persisted, d, now, policy)
		raw, err := json.Marshal(dungeonQuotaStored{Receipt: result, ContentHash: DungeonQuotaIdentity(c)})
		if err != nil {
			return err
		}
		return receipts.Append(ctx, c.Key, raw)
	})
	return result, err
}

func DecodeDungeonQuotaReceipt(raw []byte) (DungeonQuotaReceipt, error) {
	var v dungeonQuotaStored
	err := json.Unmarshal(raw, &v)
	if err == nil && v.Receipt.Kind != "dungeon-quota-operation" {
		err = ErrDungeonQuotaState
	}
	return v.Receipt, err
}
