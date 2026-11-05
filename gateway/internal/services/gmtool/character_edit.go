package gmtool

import (
	"context"
	"strconv"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
)

// Ordinary online snapshots are insufficient: the proof includes pending
// selection and cleanup, using the same DB fence as this transaction.
func (s *Server) v1CharacterEditCheck(account string) adminops.QuestOfflineCheck {
	return func(ctx context.Context, character string) error {
		if carbonMode(ctx) {
			return nil
		}
		id, err := strconv.ParseUint(character, 10, 16)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != character || account == "" || s.deps.Runtime == nil {
			return adminops.ErrRuntimeUnavailable
		}
		result, err := s.deps.Runtime.Call(ctx, adminapi.Request{Action: "character_edit_status", AccountID: account, CharacterID: uint16(id)})
		proof := result.CharacterEdit
		if err != nil || !result.OK || proof == nil || proof.Protocol != adminapi.CharacterEditProtocol || proof.AccountID != account || proof.CharacterID != uint16(id) {
			return adminops.ErrRuntimeUnavailable
		}
		if proof.Busy {
			return adminops.ErrCharacterOnline
		}
		return nil
	}
}
