package adminops

import (
	"context"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Worn instances use the same option mutator and receipt as bag items, while
// preserving Equipment as their authoritative store. The view is internal only.
func equippedOperationStorage(ctx context.Context, g repository.Group, c InventoryCommand) (repository.InventoryRecord, inventoryPersist, inventoryReload, error) {
	if g.Equipment == nil {
		return repository.InventoryRecord{}, nil, nil, ErrRuntimeUnavailable
	}
	bagCommand := c
	bagCommand.Items = nil
	worn := []InventorySelection{}
	bagFields := map[repository.InventoryField]bool{}
	for _, item := range c.Items {
		if item.Container == "equipped" {
			worn = append(worn, item)
		} else {
			bagCommand.Items = append(bagCommand.Items, item)
			spec, _, _ := InventoryTarget(item)
			field := repository.InventoryFieldSlots
			if spec.Collection == "warehouse" {
				field = repository.InventoryFieldWarehouse
			}
			bagFields[field] = true
		}
	}
	base := repository.InventoryRecord{CharacterID: c.CharacterID}
	var bagSave inventoryPersist
	var bagRead inventoryReload
	var err error
	if len(bagCommand.Items) > 0 {
		base, bagSave, bagRead, err = inventoryOperationStorage(ctx, g, bagCommand, "character")
		if err != nil {
			return base, nil, nil, err
		}
	}
	// Keep any historical bag copies of list 3 unchanged when saving bag fields.
	bagOriginal := repository.CloneInventory(base)
	var equipment repository.EquipmentRecord
	keys := map[int]string{}
	project := func(view repository.InventoryRecord) (repository.InventoryRecord, error) {
		var found bool
		equipment, found, err = g.Equipment.Load(ctx, c.CharacterID)
		if err != nil {
			return view, err
		}
		if !found || equipment.CharacterID != c.CharacterID {
			return view, ErrInventorySlot
		}
		view = repository.CloneInventory(view)
		if view.Slots == nil {
			view.Slots = map[string]repository.ItemStack{}
		}
		keys = map[int]string{}
		for _, item := range worn {
			_, key, _ := InventoryTarget(item)
			delete(view.Slots, key)
			for entryKey, entry := range equipment.Entries {
				if int(entry.SlotIndex) != item.Slot {
					continue
				}
				if _, duplicate := keys[item.Slot]; duplicate {
					return view, ErrInventoryInput
				}
				keys[item.Slot] = entryKey
				view.Slots[key] = repository.ItemStack{ItemID: entry.ItemID, Count: 1, Bind: entry.Bind, ExpireAt: entry.ExpireAt, RawEntry: entry.RawEntry, Extra: entry.Extra}
			}
		}
		return view, nil
	}
	view, err := project(base)
	if err != nil {
		return view, nil, nil, err
	}
	save := func(next repository.InventoryRecord, _ []repository.InventoryField) error {
		eq := repository.CloneEquipment(equipment)
		bag := repository.CloneInventory(next)
		if bag.Slots == nil {
			bag.Slots = map[string]repository.ItemStack{}
		}
		for _, item := range worn {
			_, key, _ := InventoryTarget(item)
			entryKey, found := keys[item.Slot]
			if !found {
				return ErrInventorySlot
			}
			stack, exists := next.Slots[key]
			if !exists {
				delete(eq.Entries, entryKey)
			} else {
				entry := eq.Entries[entryKey]
				if stack.Count != 1 {
					return ErrInventoryInput
				}
				entry.ItemID = stack.ItemID
				entry.Bind, entry.ExpireAt, entry.RawEntry, entry.Extra = stack.Bind, stack.ExpireAt, stack.RawEntry, stack.Extra
				eq.Entries[entryKey] = entry
			}
			if old, exists := bagOriginal.Slots[key]; exists {
				bag.Slots[key] = old
			} else {
				delete(bag.Slots, key)
			}
		}
		if bagSave != nil {
			fields := []repository.InventoryField{}
			for _, field := range []repository.InventoryField{repository.InventoryFieldSlots, repository.InventoryFieldWarehouse} {
				if bagFields[field] {
					fields = append(fields, field)
				}
			}
			if err := bagSave(bag, fields); err != nil {
				return err
			}
		}
		eq.UpdatedAt = next.UpdatedAt
		if writer, ok := g.Equipment.(repository.EquipmentEntryDeltaWriter); ok {
			changed := make([]string, 0, len(worn))
			for _, item := range worn {
				changed = append(changed, keys[item.Slot])
			}
			return writer.SaveEquipmentEntryDelta(ctx, eq, changed)
		}
		return repository.SaveEquipmentFields(ctx, g.Equipment, eq, repository.EquipmentFieldEntries)
	}
	reload := func() (repository.InventoryRecord, error) {
		current := repository.InventoryRecord{CharacterID: c.CharacterID}
		if bagRead != nil {
			current, err = bagRead()
			if err != nil {
				return current, err
			}
		}
		return project(current)
	}
	return view, save, reload, nil
}
