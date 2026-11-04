package repository

import (
	"context"
	"time"
)

type AdventureName struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
}

// Adventure names belong to the account. The writer exposes no metadata or
// balance save operation; rename and its receipt commit together.
type AdventureRenameRepository interface {
	LoadAdventureName(context.Context, string) (AdventureName, error)
	CheckAdventureName(context.Context, string) error
	RenameAdventure(context.Context, string, time.Time) error
}

type AdventureRenameUnitOfWork interface {
	LoadAdventureName(context.Context, string) (AdventureName, error)
	WithinAdventureRename(context.Context, string, func(AdventureRenameRepository, ManagementReceiptStore) error) error
	AdventureRenameLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}
