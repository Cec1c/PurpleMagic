package repository

import (
	"context"
	"time"
)

type DungeonQuotaManagementState struct {
	Character CharacterRecord
}

type DungeonQuotaManagementReader interface {
	LoadDungeonQuotaManagement(context.Context, string) (DungeonQuotaManagementState, error)
}

type DungeonQuotaManagementRepository interface {
	DungeonQuotaManagementReader
	ResetDungeonQuotaKeys(ctx context.Context, characterID string, deleteKeys []string, sequence int64, updatedAt time.Time) error
}

type DungeonQuotaManagementUnitOfWork interface {
	WithinDungeonQuotaManagement(context.Context, string, string, func(DungeonQuotaManagementRepository, ManagementReceiptStore) error) error
	DungeonQuotaManagementLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}
