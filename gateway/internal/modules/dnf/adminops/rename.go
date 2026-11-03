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

var ErrRenameInput = errors.New("invalid character name")

type RenameCommand struct {
	AccountID    string `json:"accountId"`
	CharacterID  string `json:"characterId"`
	ExpectedName string `json:"expectedName"`
	NewName      string `json:"newName"`
	Reason       string `json:"reason"`
	Actor        string `json:"actor"`
	Key          string `json:"-"`
}
type RenameReceipt struct {
	Kind        string    `json:"kind"`
	RequestID   string    `json:"requestId"`
	AccountID   string    `json:"accountId"`
	CharacterID string    `json:"characterId"`
	Before      string    `json:"before"`
	After       string    `json:"after"`
	Reason      string    `json:"reason"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}
type renameStored struct {
	Receipt     RenameReceipt `json:"receipt"`
	ContentHash string        `json:"contentHash"`
}

func ValidateCharacterName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return ErrRenameInput
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return ErrRenameInput
		}
	}
	b, e := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(name))
	if e != nil || len(b) < 2 || len(b) > 12 {
		return ErrRenameInput
	}
	back, e := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if e != nil || string(back) != name {
		return ErrRenameInput
	}
	return nil
}
func RenameIdentity(c RenameCommand) string {
	b, _ := json.Marshal(c)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func RunRenameOperation(ctx context.Context, u repository.CharacterRenameUnitOfWork, c RenameCommand, commit bool, offline QuestOfflineCheck) (RenameReceipt, error) {
	var result RenameReceipt
	if ValidateCharacterName(c.NewName) != nil || c.AccountID == "" || c.CharacterID == "" || len(c.ExpectedName) > 128 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 512 || (commit && (len(c.Key) < 16 || len(c.Key) > 128)) {
		return result, ErrRenameInput
	}
	if u == nil {
		return result, ErrRuntimeUnavailable
	}
	err := u.WithinCharacterRename(ctx, c.AccountID, c.CharacterID, func(r repository.CharacterRenameRepository, receipts repository.ManagementReceiptStore) error {
		if commit {
			raw, found, e := receipts.Load(ctx, c.Key)
			if e != nil {
				return e
			}
			if found {
				var old renameStored
				if json.Unmarshal(raw, &old) != nil || old.Receipt.Kind != "rename-operation" || old.ContentHash != RenameIdentity(c) {
					return ErrIdempotency
				}
				result = old.Receipt
				return nil
			}
		}
		character, found, e := r.Load(ctx, c.CharacterID)
		if e != nil {
			return e
		}
		if !found || character.AccountID != c.AccountID || character.Stats["delete_flag"] != 0 {
			return ErrAccount
		}
		if character.Name != c.ExpectedName {
			return ErrConflict
		}
		if e = r.CheckCharacterName(ctx, c.NewName); e != nil {
			return e
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		result = RenameReceipt{Kind: "rename-operation", AccountID: c.AccountID, CharacterID: c.CharacterID, Before: character.Name, After: c.NewName, Reason: c.Reason, Actor: c.Actor, CreatedAt: at}
		if !commit {
			return nil
		}
		if offline == nil {
			return ErrRuntimeUnavailable
		}
		if e = offline(ctx, c.CharacterID); e != nil {
			return e
		}
		if character.Name != c.NewName {
			if e = r.RenameCharacter(ctx, c.NewName, at); e != nil {
				return e
			}
		}
		if e = offline(ctx, c.CharacterID); e != nil {
			return e
		}
		result.RequestID = c.Key
		raw, e := json.Marshal(renameStored{result, RenameIdentity(c)})
		if e != nil {
			return e
		}
		return receipts.Append(ctx, c.Key, raw)
	})
	return result, err
}
func DecodeRenameReceipt(raw []byte) (RenameReceipt, error) {
	var v renameStored
	e := json.Unmarshal(raw, &v)
	if e == nil && v.Receipt.Kind != "rename-operation" {
		e = ErrRenameInput
	}
	return v.Receipt, e
}
