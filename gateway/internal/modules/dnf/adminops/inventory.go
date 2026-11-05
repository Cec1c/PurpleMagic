package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var ErrInventoryInput = errors.New("invalid inventory operation")
var ErrInventoryContainer = errors.New("inventory container needs its own transaction")
var ErrInventorySlot = errors.New("inventory slot missing or changed")

type InventoryOptions struct {
	ItemID            *string `json:"itemId,omitempty"`
	QualityMode       *int    `json:"qualityMode,omitempty"`
	UpgradeLevel      *int    `json:"upgradeLevel,omitempty"`
	AmplifyType       *int    `json:"amplifyType,omitempty"`
	ForgingLevel      *int    `json:"forgingLevel,omitempty"`
	AvatarOptionValue *int    `json:"avatarOptionValue,omitempty"`
	ExpirationDays    *int    `json:"expirationDays,omitempty"`
	Count             *string `json:"count,omitempty"`
}
type InventorySelection struct {
	Container       string `json:"container"`
	Slot            int    `json:"slot"`
	Revision        string `json:"revision"`
	RelatedRevision string `json:"relatedRevision,omitempty"`
}
type InventoryCommand struct {
	Force       bool                 `json:"force,omitempty"`
	AccountID   string               `json:"accountId"`
	CharacterID string               `json:"characterId"`
	Action      string               `json:"action"`
	Items       []InventorySelection `json:"items"`
	Options     *InventoryOptions    `json:"options,omitempty"`
	PVFChecksum string               `json:"pvfChecksum"`
	Reason      string               `json:"reason"`
	Actor       string               `json:"actor"`
	PlanToken   string               `json:"planToken,omitempty"`
	Key         string               `json:"-"`
}
type InventoryState struct {
	Artifact   *InventoryArtifactState        `json:"petArtifact,omitempty"`
	Pet        *repository.PetManagementState `json:"pet,omitempty"`
	Present    bool                           `json:"present"`
	ItemID     string                         `json:"itemId"`
	Count      string                         `json:"count"`
	Name       string                         `json:"name"`
	Bound      bool                           `json:"bound"`
	ExpiresAt  time.Time                      `json:"expiresAt"`
	Attributes map[string]any                 `json:"attributes"`
}
type InventoryChange struct {
	Container string          `json:"container"`
	Slot      int             `json:"slot"`
	Before    InventoryState  `json:"before"`
	After     *InventoryState `json:"after"`
	Revision  string          `json:"revision"`
}
type InventoryReceipt struct {
	Forced      bool              `json:"forced,omitempty"`
	OwnerScope  string            `json:"ownerScope"`
	RequestID   string            `json:"requestId,omitempty"`
	Kind        string            `json:"kind"`
	AccountID   string            `json:"accountId"`
	CharacterID string            `json:"characterId"`
	Action      string            `json:"action"`
	Changes     []InventoryChange `json:"changes"`
	PVFChecksum string            `json:"pvfChecksum"`
	Reason      string            `json:"reason"`
	Actor       string            `json:"actor"`
	CreatedAt   time.Time         `json:"createdAt"`
}
type InventoryOperationError struct {
	Container string
	Slot      int
	Err       error
}

func (e *InventoryOperationError) Error() string {
	return fmt.Sprintf("%s/%d: %v", e.Container, e.Slot, e.Err)
}
func (e *InventoryOperationError) Unwrap() error { return e.Err }

type inventoryStored struct {
	Receipt     InventoryReceipt `json:"receipt"`
	ContentHash string           `json:"contentHash"`
}
type InventoryMutator func(context.Context, InventorySelection, repository.ItemStack, *InventoryOptions) (repository.ItemStack, error)
type InventoryProjector func(InventorySelection, repository.ItemStack) InventoryState

func InventoryRevision(account, character, container string, slot int, stack repository.ItemStack) string {
	if spec, ok := repository.ResourceContainer(container); ok && spec.Owner == "account" {
		character = ""
	}
	raw, _ := json.Marshal([]any{account, character, container, slot, stack})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func InventorySlotRevision(account, character, container string, slot int, stack repository.ItemStack, present bool) string {
	if present {
		return InventoryRevision(account, character, container, slot, stack)
	}
	// Missing rows and persisted zero-count rows are distinct observed states.
	base := InventoryRevision(account, character, container, slot, repository.ItemStack{})
	sum := sha256.Sum256([]byte("absent-inventory-slot\x00" + base))
	return hex.EncodeToString(sum[:])
}
func InventoryIdentity(c InventoryCommand) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func InventoryTarget(v InventorySelection) (repository.ContainerSpec, string, error) {
	spec, ok := repository.ResourceContainer(v.Container)
	if !ok || (spec.Owner != "character" && spec.Owner != "account" && spec.Owner != "equipment") {
		return spec, "", ErrInventoryContainer
	}
	if v.Slot < spec.First || v.Slot > spec.Last || (v.Container == "equipped" && v.Slot > 32) {
		return spec, "", ErrInventoryInput
	}
	return spec, fmt.Sprintf("%d:%d", spec.List, v.Slot), nil
}

func InventoryScope(items []InventorySelection) (string, error) {
	scope := ""
	for _, item := range items {
		spec, _, err := InventoryTarget(item)
		if err != nil {
			return "", err
		}
		// Worn equipment and bag inventory belong to the same character transaction.
		if spec.Owner == "equipment" {
			spec.Owner = "character"
		}
		if scope != "" && scope != spec.Owner {
			return "", ErrInventoryInput
		}
		scope = spec.Owner
	}
	if scope == "" {
		return "", ErrInventoryInput
	}
	return scope, nil
}
func validInventoryCommand(c InventoryCommand, commit bool) error {
	n, e := strconv.ParseUint(c.CharacterID, 10, 16)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != c.CharacterID || c.AccountID == "" || c.Actor == "" || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 500 || len(c.Items) < 1 || len(c.Items) > 200 || len(c.PVFChecksum) != 64 {
		return ErrInventoryInput
	}
	if _, e = hex.DecodeString(c.PVFChecksum); e != nil {
		return ErrInventoryInput
	}
	if c.Action != "delete" && c.Action != "configure" {
		return ErrInventoryInput
	}
	if (c.Action == "delete" && c.Options != nil) || (c.Action == "configure" && c.Options == nil) {
		return ErrInventoryInput
	}
	if c.Options != nil && c.Options.ItemID != nil {
		o := c.Options
		id, err := strconv.ParseUint(*o.ItemID, 10, 32)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != *o.ItemID || len(c.Items) != 1 || o.Count != nil || o.QualityMode != nil || o.UpgradeLevel != nil || o.AmplifyType != nil || o.ForgingLevel != nil || o.AvatarOptionValue != nil || o.ExpirationDays != nil {
			return ErrInventoryInput
		}
		v := c.Items[0]
		if v.Container == "shared" || InventoryPetTarget(v) || InventoryArtifactTarget(v) {
			return ErrInventoryContainer
		}
	}
	if commit && (len(c.Key) < 16 || len(c.Key) > 128 || len(c.PlanToken) == 0) {
		return ErrInventoryInput
	}
	if _, e = InventoryScope(c.Items); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, v := range c.Items {
		_, key, e := InventoryTarget(v)
		if e != nil {
			return &InventoryOperationError{v.Container, v.Slot, e}
		}
		if seen[key] || len(v.Revision) != 64 {
			return ErrInventoryInput
		}
		seen[key] = true
		if _, e = hex.DecodeString(v.Revision); e != nil {
			return ErrInventoryInput
		}
		if InventoryPetTarget(v) || InventoryArtifactTarget(v) {
			if len(v.RelatedRevision) != 64 {
				return ErrInventoryInput
			}
			if _, e = hex.DecodeString(v.RelatedRevision); e != nil {
				return ErrInventoryInput
			}
		} else if v.RelatedRevision != "" {
			return ErrInventoryInput
		}
	}
	return nil
}
func loadInventoryReceipt(ctx context.Context, r repository.ManagementReceiptStore, c InventoryCommand) (InventoryReceipt, bool, error) {
	raw, found, e := r.Load(ctx, c.Key)
	return decodeInventoryReceiptLookup(raw, found, e, c)
}

func decodeInventoryReceiptLookup(raw []byte, found bool, e error, c InventoryCommand) (InventoryReceipt, bool, error) {
	var old inventoryStored
	if e != nil || !found {
		return old.Receipt, found, e
	}
	if e = json.Unmarshal(raw, &old); e != nil {
		return old.Receipt, true, e
	}
	if old.ContentHash != InventoryIdentity(c) {
		return InventoryReceipt{}, true, ErrIdempotency
	}
	return old.Receipt, true, nil
}
func LookupInventoryOperation(ctx context.Context, u repository.InventoryManagementUnitOfWork, c InventoryCommand) (InventoryReceipt, bool, error) {
	var result InventoryReceipt
	var found bool
	if e := validInventoryCommand(c, true); e != nil {
		return result, false, e
	}
	if u == nil {
		return result, false, ErrRuntimeUnavailable
	}
	if reader, ok := u.(repository.InventoryReceiptReader); ok {
		raw, found, err := reader.LookupInventoryReceipt(ctx, c.AccountID, c.CharacterID, c.Key)
		return decodeInventoryReceiptLookup(raw, found, err, c)
	}
	e := u.WithinInventoryManagement(ctx, c.AccountID, c.CharacterID, func(g repository.Group, r repository.ManagementReceiptStore) error {
		ch, ok, e := g.Character.Load(ctx, c.CharacterID)
		if e != nil {
			return e
		}
		if !ok || ch.AccountID != c.AccountID || ch.Stats["delete_flag"] != 0 {
			return ErrAccount
		}
		result, found, e = loadInventoryReceipt(ctx, r, c)
		return e
	})
	return result, found, e
}
func RunInventoryOperation(ctx context.Context, u repository.InventoryManagementUnitOfWork, c InventoryCommand, commit bool, mutate InventoryMutator, project InventoryProjector, offline QuestOfflineCheck) (InventoryReceipt, error) {
	var result InventoryReceipt
	if e := validInventoryCommand(c, commit); e != nil {
		return result, e
	}
	if u == nil || project == nil {
		return result, ErrRuntimeUnavailable
	}
	e := u.WithinInventoryManagement(ctx, c.AccountID, c.CharacterID, func(g repository.Group, r repository.ManagementReceiptStore) error {
		ch, ok, e := g.Character.Load(ctx, c.CharacterID)
		if e != nil {
			return e
		}
		if !ok || ch.AccountID != c.AccountID || ch.Stats["delete_flag"] != 0 {
			return ErrAccount
		}
		if commit {
			old, found, e := loadInventoryReceipt(ctx, r, c)
			if e != nil {
				return e
			}
			if found {
				result = old
				return nil
			}
		}
		scope, _ := InventoryScope(c.Items)
		// Acquire the same inventory -> equipment -> pet lock order as gameplay.
		pets, e := prepareInventoryPetChange(ctx, g, c)
		if e != nil {
			return e
		}
		original, persist, reload, e := inventoryOperationStorage(ctx, g, c, scope)
		if e != nil {
			return e
		}
		next := repository.CloneInventory(original)
		if next.Slots == nil {
			next.Slots = map[string]repository.ItemStack{}
		}
		if commit {
			if offline == nil {
				return ErrRuntimeUnavailable
			}
			if e = inventoryOperationOffline(ctx, g, c, scope, offline); e != nil {
				return e
			}
		}
		result = InventoryReceipt{Forced: c.Force, OwnerScope: scope, Kind: "inventory-operation", AccountID: c.AccountID, CharacterID: c.CharacterID, Action: c.Action, PVFChecksum: c.PVFChecksum, Reason: c.Reason, Actor: c.Actor, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Changes: []InventoryChange{}}
		fields := map[repository.InventoryField]bool{}
		for _, v := range c.Items {
			if e = ctx.Err(); e != nil {
				return e
			}
			spec, key, _ := InventoryTarget(v)
			entries := next.Slots
			field := repository.InventoryFieldSlots
			if spec.Collection == "warehouse" {
				entries = next.Warehouse
				field = repository.InventoryFieldWarehouse
			}
			stack, ok := entries[key]
			if v.Container != "shared" && (!ok || stack.ItemID <= 0 || stack.Count <= 0) {
				return &InventoryOperationError{v.Container, v.Slot, ErrInventorySlot}
			}
			if InventorySlotRevision(c.AccountID, c.CharacterID, v.Container, v.Slot, stack, ok) != v.Revision {
				return &InventoryOperationError{v.Container, v.Slot, ErrConflict}
			}
			if v.Container == "shared" {
				id, valid := repository.AccountWarehouseItemForSlot(int16(v.Slot))
				if !valid || (ok && (id != stack.ItemID || stack.Count < 0)) {
					return &InventoryOperationError{v.Container, v.Slot, ErrInventoryInput}
				}
				if !ok {
					stack = repository.ItemStack{ItemID: id}
				}
			}
			change := InventoryChange{Container: v.Container, Slot: v.Slot, Before: project(v, stack)}
			if pets != nil {
				if state, ok := pets.states[key]; ok {
					stateCopy := state
					change.Before.Pet = &stateCopy
				}
			}
			if pets != nil && InventoryArtifactTarget(v) {
				change.Before.Artifact = &InventoryArtifactState{Color: pets.artifacts[v.Slot], Slot: v.Slot}
			}
			change.Before.Present = ok
			if c.Action == "delete" {
				delete(entries, key)
			} else {
				if mutate == nil {
					return ErrRuntimeUnavailable
				}
				updated, e := mutate(ctx, v, stack, c.Options)
				if e != nil {
					return &InventoryOperationError{v.Container, v.Slot, e}
				}
				expectedID := stack.ItemID
				if c.Options.ItemID != nil {
					expectedID, _ = strconv.ParseInt(*c.Options.ItemID, 10, 64)
				}
				if updated.ItemID != expectedID || updated.Count < 0 || (updated.Count == 0 && v.Container != "shared") {
					return ErrInventoryInput
				}
				if updated.Count == 0 {
					delete(entries, key)
				} else {
					entries[key] = updated
				}
				state := project(v, updated)
				state.Present = updated.Count > 0
				change.After = &state
				change.Revision = InventorySlotRevision(c.AccountID, c.CharacterID, v.Container, v.Slot, updated, state.Present)
			}
			if pets != nil && InventoryArtifactTarget(v) {
				if e = pets.updateArtifact(v, next); e != nil {
					return &InventoryOperationError{v.Container, v.Slot, e}
				}
				if change.After != nil {
					change.After.Artifact = change.Before.Artifact
				}
			}
			fields[field] = true
			result.Changes = append(result.Changes, change)
		}
		if !commit {
			return nil
		}
		next.UpdatedAt = result.CreatedAt
		changed := []repository.InventoryField{}
		for _, f := range []repository.InventoryField{repository.InventoryFieldSlots, repository.InventoryFieldWarehouse} {
			if fields[f] {
				changed = append(changed, f)
			}
		}
		if e = persist(next, changed); e != nil {
			return e
		}
		if pets != nil {
			if e = pets.persist(ctx, g.Pet, result.CreatedAt); e != nil {
				return e
			}
		}
		if e = inventoryOperationOffline(ctx, g, c, scope, offline); e != nil {
			return e
		}
		persisted, e := reload()
		if e != nil {
			return e
		}
		for _, v := range c.Items {
			spec, key, _ := InventoryTarget(v)
			got, want := persisted.Slots, next.Slots
			if spec.Collection == "warehouse" {
				got, want = persisted.Warehouse, next.Warehouse
			}
			a, aok := got[key]
			b, bok := want[key]
			if aok != bok || InventoryRevision(c.AccountID, c.CharacterID, v.Container, v.Slot, a) != InventoryRevision(c.AccountID, c.CharacterID, v.Container, v.Slot, b) {
				return ErrConflict
			}
		}
		result.RequestID = c.Key
		payload, e := json.Marshal(inventoryStored{Receipt: result, ContentHash: InventoryIdentity(c)})
		if e != nil {
			return e
		}
		return r.Append(ctx, c.Key, payload)
	})
	return result, e
}
func DecodeInventoryReceipt(raw []byte) (InventoryReceipt, error) {
	var v inventoryStored
	e := json.Unmarshal(raw, &v)
	return v.Receipt, e
}
