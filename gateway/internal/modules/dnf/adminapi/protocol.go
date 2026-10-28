// Package adminapi defines the private game-runtime management contract.
// It contains no web panel, database connection, scheduler or game transport.
package adminapi

import (
	"dnf90.local/gateway/internal/modules/dnf/moderation"
	"dnf90.local/gateway/internal/modules/dnf/onlineevent"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const Version = 1
const Path = "/api/game/admin"

type Request struct {
	ModerationQuery *moderation.Query           `json:"moderationQuery,omitempty"`
	BanChange       *moderation.BanChange       `json:"banChange,omitempty"`
	AccountID       string                      `json:"accountId,omitempty"`
	Version         int                         `json:"version"`
	Action          string                      `json:"action"`
	CharacterID     uint16                      `json:"characterId,omitempty"`
	Revision        uint64                      `json:"revision,omitempty"`
	Changes         []onlineevent.ControlChange `json:"changes,omitempty"`
	ListType        byte                        `json:"listType,omitempty"`
	Slot            int16                       `json:"slot,omitempty"`
	Stack           *repository.ItemStack       `json:"stack,omitempty"`
}

type Response struct {
	Clears             *moderation.ClearPage        `json:"clears,omitempty"`
	Bans               *moderation.BanPage          `json:"bans,omitempty"`
	Ban                *moderation.Ban              `json:"ban,omitempty"`
	ModerationAccounts *moderation.AccountPage      `json:"moderationAccounts,omitempty"`
	CharacterEdit      *CharacterEditStatus         `json:"characterEdit,omitempty"`
	AccountEdit        *AccountEditStatus           `json:"accountEdit,omitempty"`
	OnlineSnapshot     *OnlineSnapshot              `json:"onlineSnapshot,omitempty"`
	Version            int                          `json:"version"`
	OK                 bool                         `json:"ok"`
	Error              string                       `json:"error,omitempty"`
	Online             bool                         `json:"online"`
	Activities         *onlineevent.ControlSnapshot `json:"activities,omitempty"`
	PremiumTypes       []int64                      `json:"premiumTypes,omitempty"`
	CrystalCubeItemIDs [6]int64                     `json:"crystalCubeItemIds,omitempty"`
	StackRow           []byte                       `json:"stackRow,omitempty"`
}
