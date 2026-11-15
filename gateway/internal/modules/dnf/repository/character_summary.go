package repository

import (
	"context"
	"errors"
	"time"
)

var ErrCharacterSummaryRange = errors.New("character summary range or stored ownership is invalid")

// CharacterSummary contains only the authoritative base columns needed by an
// administrative directory. It never loads stats, roster, equipment or assets.
type CharacterSummary struct {
	CharacterID   string    `json:"characterId"`
	AccountID     string    `json:"accountId"`
	AccountName   string    `json:"accountName,omitempty"`
	AccountBanned *bool     `json:"accountBanned,omitempty"`
	Name          string    `json:"name"`
	Job           string    `json:"job"`
	Level         int       `json:"level"`
	GrowType      int       `json:"growType"`
	Slot          int       `json:"slot"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// This optional read model deliberately has no full-aggregate fallback.
type CharacterSummaryReader interface {
	SummariesForAccounts(context.Context, []string) ([]CharacterSummary, error)
	SearchSummaries(context.Context, string, string, int) ([]CharacterSummary, error)
	FindSummary(context.Context, string) (CharacterSummary, bool, error)
}

// IDs come from a bounded authoritative selection (for example a game snapshot).
type CharacterBatchSummaryReader interface {
	SummariesForCharacters(context.Context, []string) ([]CharacterSummary, error)
}
