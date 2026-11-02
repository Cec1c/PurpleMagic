package repository

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
)

// Management reads existing settlement history without calling the game process.
type ModerationStatsReader interface {
	QueryClears(context.Context, moderation.Query) (moderation.ClearPage, error)
	QueryAccountClears(context.Context, moderation.Query) ([]moderation.AccountClearStats, error)
}
