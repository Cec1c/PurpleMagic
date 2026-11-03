package repository

import (
	"context"
	"errors"
	"time"
)

var ErrCharacterNameTaken = errors.New("character name already taken")

// The writer changes only the selected character's name and timestamp. Name
// lookup and the write share the fenced transaction and its durable receipt.
type CharacterRenameRepository interface {
	Load(context.Context, string) (CharacterRecord, bool, error)
	CheckCharacterName(context.Context, string) error
	RenameCharacter(context.Context, string, time.Time) error
}

type CharacterRenameUnitOfWork interface {
	WithinCharacterRename(context.Context, string, string, func(CharacterRenameRepository, ManagementReceiptStore) error) error
	CharacterRenameLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}
