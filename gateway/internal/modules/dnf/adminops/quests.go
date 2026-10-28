package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var ErrQuestInput = errors.New("invalid quest operation input")
var ErrCharacterOnline = errors.New("character is online")
var ErrRuntimeUnavailable = errors.New("game runtime unavailable")

type QuestCommand struct {
	AccountID   string  `json:"accountId"`
	CharacterID string  `json:"characterId"`
	Action      string  `json:"action"`
	QuestIDs    []int64 `json:"questIds"`
	Revision    string  `json:"revision"`
	PVFChecksum string  `json:"pvfChecksum"`
	Reason      string  `json:"reason"`
	Actor       string  `json:"actor"`
	Key         string  `json:"-"`
}
type QuestChange struct {
	QuestID        string `json:"questId"`
	Before         string `json:"before"`
	After          string `json:"after"`
	ProgressBefore string `json:"progressBefore"`
	ProgressAfter  string `json:"progressAfter"`
	Changed        bool   `json:"changed"`
}
type QuestReceipt struct {
	RequestID      string               `json:"requestId,omitempty"`
	Kind           string               `json:"kind"`
	AccountID      string               `json:"accountId"`
	CharacterID    string               `json:"characterId"`
	Action         string               `json:"action"`
	Revision       string               `json:"revision"`
	PVFChecksum    string               `json:"pvfChecksum"`
	Changes        []QuestChange        `json:"changes"`
	RewardGranted  bool                 `json:"rewardGranted"`
	EquipmentSlots *QuestEquipmentSlots `json:"equipmentSlots,omitempty"`
	Reason         string               `json:"reason"`
	Actor          string               `json:"actor"`
	CreatedAt      time.Time            `json:"createdAt"`
}
type QuestEquipmentSlots struct {
	Before    int64 `json:"before"`
	After     int64 `json:"after"`
	Requested int64 `json:"requested"`
}
type questStored struct {
	Receipt     QuestReceipt `json:"receipt"`
	ContentHash string       `json:"contentHash"`
}
type QuestOfflineCheck func(context.Context, string) error

// LookupQuestOperation recovers a committed request independently of the current
// PVF or game runtime. The exact original command and owner must still match.
func LookupQuestOperation(ctx context.Context, u repository.CharacterManagementUnitOfWork, c QuestCommand) (QuestReceipt, bool, error) {
	var result QuestReceipt
	var found bool
	if err := validQuestCommand(c, true); err != nil {
		return result, false, err
	}
	if u == nil {
		return result, false, ErrRuntimeUnavailable
	}
	err := u.WithinCharacterManagement(ctx, c.AccountID, c.CharacterID, func(g repository.Group, receipts repository.ManagementReceiptStore) error {
		character, exists, err := g.Character.Load(ctx, c.CharacterID)
		if err != nil {
			return err
		}
		if !exists || character.AccountID != c.AccountID || character.Stats["delete_flag"] != 0 {
			return ErrAccount
		}
		result, found, err = loadQuestReceipt(ctx, receipts, c)
		return err
	})
	return result, found, err
}

func questIdentity(c QuestCommand) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func loadQuestReceipt(ctx context.Context, receipts repository.ManagementReceiptStore, c QuestCommand) (QuestReceipt, bool, error) {
	var old questStored
	raw, found, err := receipts.Load(ctx, c.Key)
	if err != nil || !found {
		return old.Receipt, found, err
	}
	if err = json.Unmarshal(raw, &old); err != nil {
		return old.Receipt, true, err
	}
	if old.ContentHash != questIdentity(c) {
		return QuestReceipt{}, true, ErrIdempotency
	}
	return old.Receipt, true, nil
}

func QuestRevision(c repository.CharacterRecord, q repository.QuestRecord, pvf string) string {
	raw, _ := json.Marshal([]any{c.CharacterID, c.AccountID, c.Level, c.Job, c.Stats["grow_type"], c.Stats["ex_equip_slot_stat"], q.States, q.Progress, pvf})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validQuestCommand(c QuestCommand, commit bool) error {
	n, e := strconv.ParseUint(c.CharacterID, 10, 16)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != c.CharacterID || c.AccountID == "" || len(c.QuestIDs) == 0 || len(c.QuestIDs) > 200 || len(c.PVFChecksum) != 64 || len(c.Reason) > 500 || strings.TrimSpace(c.Reason) == "" || c.Actor == "" {
		return ErrQuestInput
	}
	if _, e = hex.DecodeString(c.PVFChecksum); e != nil {
		return ErrQuestInput
	}
	seen := map[int64]bool{}
	for _, id := range c.QuestIDs {
		if id <= 0 || id > 65535 || seen[id] {
			return ErrQuestInput
		}
		seen[id] = true
	}
	switch c.Action {
	case "accept", "give-up", "ready", "complete", "reset":
	default:
		return ErrQuestInput
	}
	if commit {
		if len(c.Revision) != 64 || len(c.Key) < 16 || len(c.Key) > 128 {
			return ErrQuestInput
		}
		if _, e = hex.DecodeString(c.Revision); e != nil {
			return ErrQuestInput
		}
	}
	return nil
}

func RunQuestOperation(ctx context.Context, u repository.CharacterManagementUnitOfWork, catalog *quest.Catalog, c QuestCommand, commit bool, offline QuestOfflineCheck) (QuestReceipt, error) {
	var result QuestReceipt
	if err := validQuestCommand(c, commit); err != nil {
		return result, err
	}
	if u == nil || catalog == nil {
		return result, ErrRuntimeUnavailable
	}
	identity := questIdentity(c)
	err := u.WithinCharacterManagement(ctx, c.AccountID, c.CharacterID, func(g repository.Group, receipts repository.ManagementReceiptStore) error {
		character, found, e := g.Character.Load(ctx, c.CharacterID)
		if e != nil {
			return e
		}
		if !found || character.CharacterID != c.CharacterID || character.AccountID != c.AccountID || character.Stats["delete_flag"] != 0 {
			return ErrAccount
		}
		if commit {
			old, found, e := loadQuestReceipt(ctx, receipts, c)
			if e != nil {
				return e
			}
			if found {
				result = old
				return nil
			}
		}
		q, found, e := g.Quest.Load(ctx, c.CharacterID)
		if e != nil {
			return e
		}
		if !found {
			q = repository.QuestRecord{CharacterID: c.CharacterID}
		}
		if q.CharacterID != c.CharacterID {
			return ErrAccount
		}
		revision := QuestRevision(character, q, c.PVFChecksum)
		if commit && revision != c.Revision {
			return ErrConflict
		}
		if commit {
			if offline == nil {
				return ErrRuntimeUnavailable
			}
			if e = offline(ctx, c.CharacterID); e != nil {
				return e
			}
		}
		next, e := quest.PlanAdministration(ctx, catalog, character, q, c.Action, c.QuestIDs)
		if e != nil {
			return e
		}
		result = QuestReceipt{Kind: "quest-operation", AccountID: c.AccountID, CharacterID: c.CharacterID, Action: c.Action, Revision: revision, PVFChecksum: c.PVFChecksum, Changes: questChanges(q, next, c.QuestIDs), Reason: c.Reason, Actor: c.Actor, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		bits, e := quest.AdministrationSlotExpansion(catalog, character, c.Action, c.QuestIDs)
		if e != nil {
			return e
		}
		if bits != 0 {
			before := character.Stats["ex_equip_slot_stat"]
			result.EquipmentSlots = &QuestEquipmentSlots{Before: before, After: before | int64(bits), Requested: int64(bits)}
		}
		if !commit {
			return nil
		}
		if slots := result.EquipmentSlots; slots != nil && slots.After != slots.Before {
			writer, ok := g.Character.(repository.CharacterEquipmentSlotWriter)
			if !ok {
				return ErrRuntimeUnavailable
			}
			if e = writer.UnlockEquipmentSlots(ctx, c.CharacterID, bits); e != nil {
				return e
			}
			if character.Stats == nil {
				character.Stats = map[string]int64{}
			}
			character.Stats["ex_equip_slot_stat"] = slots.After
		}
		if e = repository.SaveQuestFields(ctx, g.Quest, next, repository.QuestFieldStates, repository.QuestFieldProgress); e != nil {
			return e
		}
		if e = offline(ctx, c.CharacterID); e != nil {
			return e
		}
		persisted, found, e := g.Quest.Load(ctx, c.CharacterID)
		if e != nil {
			return e
		}
		if !found {
			return ErrAccount
		}
		result.Revision = QuestRevision(character, persisted, c.PVFChecksum)
		result.RequestID = c.Key
		payload, e := json.Marshal(questStored{Receipt: result, ContentHash: identity})
		if e != nil {
			return e
		}
		return receipts.Append(ctx, c.Key, payload)
	})
	return result, err
}

func questChanges(before, after repository.QuestRecord, selected []int64) []QuestChange {
	ids := map[int64]bool{}
	for _, id := range selected {
		ids[id] = true
	}
	for _, states := range []map[int64]repository.QuestState{before.States, before.Progress, after.States, after.Progress} {
		for id := range states {
			if _, exists := ids[id]; !exists {
				ids[id] = false
			}
		}
	}
	keys := make([]int64, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	state := func(q repository.QuestRecord, id int64) (repository.QuestState, bool) {
		v, ok := q.States[id]
		if !ok {
			v, ok = q.Progress[id]
		}
		return v, ok
	}
	out := []QuestChange{}
	for _, id := range keys {
		b, bok := state(before, id)
		a, aok := state(after, id)
		changed := bok != aok || !reflect.DeepEqual(b, a)
		if !changed && !ids[id] {
			continue
		}
		bs, as := "unaccepted", "unaccepted"
		if bok {
			bs = b.Status
		}
		if aok {
			as = a.Status
		}
		out = append(out, QuestChange{QuestID: strconv.FormatInt(id, 10), Before: bs, After: as, ProgressBefore: strconv.FormatInt(b.ProgressValue, 10), ProgressAfter: strconv.FormatInt(a.ProgressValue, 10), Changed: changed})
	}
	return out
}

func DecodeQuestReceipt(raw []byte) (QuestReceipt, error) {
	var stored questStored
	err := json.Unmarshal(raw, &stored)
	return stored.Receipt, err
}
