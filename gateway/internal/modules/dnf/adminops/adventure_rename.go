package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var ErrAdventureNameInput = errors.New("invalid adventure name")
var ErrAdventureNotRegistered = errors.New("adventure name not registered")

type AdventureRenameCommand struct {
	AccountID    string `json:"accountId"`
	ExpectedName string `json:"expectedName"`
	NewName      string `json:"newName"`
	Reason       string `json:"reason"`
	Actor        string `json:"actor"`
	Key          string `json:"-"`
}
type AdventureRenameReceipt struct {
	Kind      string    `json:"kind"`
	RequestID string    `json:"requestId"`
	AccountID string    `json:"accountId"`
	Before    string    `json:"before"`
	After     string    `json:"after"`
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"createdAt"`
}
type adventureRenameStored struct {
	Receipt     AdventureRenameReceipt `json:"receipt"`
	ContentHash string                 `json:"contentHash"`
}

// Matches the game's adventure-name DSTR contract: 1..16 GB18030 bytes,
// no spaces or control characters. Character names have a separate limit.
func ValidateAdventureName(name string) error {
	if name == "" {
		return ErrAdventureNameInput
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return ErrAdventureNameInput
		}
	}
	b, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(name))
	if err != nil || len(b) < 1 || len(b) > 16 {
		return ErrAdventureNameInput
	}
	back, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b)
	if err != nil || string(back) != name {
		return ErrAdventureNameInput
	}
	return nil
}
func adventureRenameIdentity(c AdventureRenameCommand) string {
	b, _ := json.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func RunAdventureRename(ctx context.Context, u repository.AdventureRenameUnitOfWork, c AdventureRenameCommand, commit bool, offline QuestOfflineCheck) (AdventureRenameReceipt, error) {
	var result AdventureRenameReceipt
	if ValidateAdventureName(c.NewName) != nil || c.AccountID == "" || len(c.ExpectedName) > 128 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 512 || (commit && (len(c.Key) < 16 || len(c.Key) > 128)) {
		return result, ErrAdventureNameInput
	}
	if u == nil {
		return result, ErrRuntimeUnavailable
	}
	err := u.WithinAdventureRename(ctx, c.AccountID, func(r repository.AdventureRenameRepository, receipts repository.ManagementReceiptStore) error {
		if commit {
			raw, found, err := receipts.Load(ctx, c.Key)
			if err != nil {
				return err
			}
			if found {
				var old adventureRenameStored
				if json.Unmarshal(raw, &old) != nil || old.Receipt.Kind != "adventure-rename-operation" || old.ContentHash != adventureRenameIdentity(c) {
					return ErrIdempotency
				}
				result = old.Receipt
				return nil
			}
		}
		state, err := r.LoadAdventureName(ctx, c.AccountID)
		if err != nil {
			return err
		}
		if state.AccountID != c.AccountID {
			return ErrAccount
		}
		if state.Name == "" {
			return ErrAdventureNotRegistered
		}
		if state.Name != c.ExpectedName {
			return ErrConflict
		}
		if err = r.CheckAdventureName(ctx, c.NewName); err != nil {
			return err
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		result = AdventureRenameReceipt{Kind: "adventure-rename-operation", AccountID: c.AccountID, Before: state.Name, After: c.NewName, Reason: c.Reason, Actor: c.Actor, CreatedAt: at}
		if !commit {
			return nil
		}
		if offline == nil {
			return ErrRuntimeUnavailable
		}
		if err = offline(ctx, c.AccountID); err != nil {
			return err
		}
		if state.Name != c.NewName {
			if err = r.RenameAdventure(ctx, c.NewName, at); err != nil {
				return err
			}
		}
		if err = offline(ctx, c.AccountID); err != nil {
			return err
		}
		result.RequestID = c.Key
		raw, err := json.Marshal(adventureRenameStored{result, adventureRenameIdentity(c)})
		if err != nil {
			return err
		}
		return receipts.Append(ctx, c.Key, raw)
	})
	return result, err
}
func DecodeAdventureRenameReceipt(raw []byte) (AdventureRenameReceipt, error) {
	var v adventureRenameStored
	err := json.Unmarshal(raw, &v)
	if err == nil && v.Receipt.Kind != "adventure-rename-operation" {
		err = ErrAdventureNameInput
	}
	return v.Receipt, err
}
