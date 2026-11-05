package adminops

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type inventoryPersist func(repository.InventoryRecord, []repository.InventoryField) error
type inventoryReload func() (repository.InventoryRecord, error)

// The internal view lets the same instance operation use either durable owner.
// Account slots are saved only through AccountInventory, never a role's rows.
func inventoryOperationStorage(ctx context.Context, g repository.Group, c InventoryCommand, scope string) (repository.InventoryRecord, inventoryPersist, inventoryReload, error) {
	for _, item := range c.Items {
		if item.Container == "equipped" {
			return equippedOperationStorage(ctx, g, c)
		}
	}
	var read inventoryReload
	var save inventoryPersist
	if scope == "account" {
		if g.AccountInventory == nil {
			return repository.InventoryRecord{}, nil, nil, ErrRuntimeUnavailable
		}
		read = func() (repository.InventoryRecord, error) {
			a, ok, err := g.AccountInventory.Load(ctx, c.AccountID)
			if err != nil {
				return repository.InventoryRecord{}, err
			}
			if !ok {
				return repository.InventoryRecord{CharacterID: c.CharacterID, Slots: map[string]repository.ItemStack{}}, nil
			}
			if a.AccountID != c.AccountID {
				return repository.InventoryRecord{}, ErrInventorySlot
			}
			return repository.InventoryRecord{CharacterID: c.CharacterID, Slots: a.Slots, UpdatedAt: a.UpdatedAt}, nil
		}
		save = func(v repository.InventoryRecord, _ []repository.InventoryField) error {
			keys := make([]string, 0, len(c.Items))
			for _, item := range c.Items {
				_, key, err := InventoryTarget(item)
				if err != nil {
					return err
				}
				keys = append(keys, key)
			}
			return repository.SaveAccountInventorySlots(ctx, g.AccountInventory, repository.AccountInventoryRecord{AccountID: c.AccountID, Slots: v.Slots, UpdatedAt: v.UpdatedAt}, keys)
		}
	} else {
		if g.Inventory == nil {
			return repository.InventoryRecord{}, nil, nil, ErrRuntimeUnavailable
		}
		read = func() (repository.InventoryRecord, error) {
			v, ok, err := g.Inventory.Load(ctx, c.CharacterID)
			if err != nil {
				return v, err
			}
			if !ok || v.CharacterID != c.CharacterID {
				return v, ErrInventorySlot
			}
			return v, nil
		}
		save = func(v repository.InventoryRecord, fields []repository.InventoryField) error {
			return repository.SaveInventoryFields(ctx, g.Inventory, v, fields...)
		}
	}
	v, err := read()
	return v, save, read, err
}

func inventoryOperationOffline(ctx context.Context, g repository.Group, c InventoryCommand, scope string, offline QuestOfflineCheck) error {
	if offline == nil {
		return ErrRuntimeUnavailable
	}
	if scope != "account" {
		return offline(ctx, c.CharacterID)
	}
	reader, ok := g.Character.(repository.InventoryManagementAccountCharacters)
	if !ok {
		return ErrRuntimeUnavailable
	}
	roles, err := reader.ManagementAccountCharacters(ctx, c.AccountID)
	if err != nil {
		return err
	}
	if len(roles) == 0 || len(roles) > repository.DefaultCharacterSlotLimit {
		return ErrAccount
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if role.AccountID != c.AccountID || seen[role.CharacterID] {
			return ErrAccount
		}
		seen[role.CharacterID] = true
		if err = offline(ctx, role.CharacterID); err != nil {
			return err
		}
	}
	if !seen[c.CharacterID] {
		return ErrAccount
	}
	return nil
}
