package mysql

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Management always writes the explicitly selected slots. Keep this adapter
// local to the GM transaction; gameplay's persistence policy is independent.
type managedInventoryStore struct {
	*characterSettlementScopedInventoryRepository
}

func (s *managedInventoryStore) SaveItemStackSlotDelta(ctx context.Context, record repository.InventoryRecord, collection string, keys []string) error {
	if err := repository.TransactionKeyError(ctx, s.characterID, record.CharacterID); err != nil {
		return err
	}
	if len(keys) == 0 {
		return ErrMySQLConfigInvalid
	}
	return s.base.(repository.ItemStackSlotDeltaWriter).SaveItemStackSlotDelta(ctx, record, collection, keys)
}

type managedEquipmentStore struct {
	*characterSettlementScopedEquipmentRepository
}

func (s *managedEquipmentStore) SaveEquipmentEntryDelta(ctx context.Context, record repository.EquipmentRecord, keys []string) error {
	if err := repository.TransactionKeyError(ctx, s.characterID, record.CharacterID); err != nil {
		return err
	}
	if len(keys) == 0 {
		return ErrMySQLConfigInvalid
	}
	return s.base.(repository.EquipmentEntryDeltaWriter).SaveEquipmentEntryDelta(ctx, record, keys)
}
