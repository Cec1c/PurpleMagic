package repository

import "context"

// ManagementReceiptStore participates in the same transaction as player data.
type ManagementReceiptStore interface {
	Load(context.Context, string) ([]byte, bool, error)
	Append(context.Context, string, []byte) error
}

type ManagementLogRow struct {
	Sequence int64
	Payload  []byte
}

type AccountManagementUnitOfWork interface {
	WithinAccountManagement(context.Context, string, func(AccountRepository, ManagementReceiptStore) error) error
	AccountManagementLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}

// AccountCurrencyWriter is exposed by the account management transaction. It
// saves only the wallet balance/version and timestamp, never the account's
// aggregate metadata. The receipt is committed in that same transaction.
type AccountCurrencyWriter interface {
	SaveAccountCurrency(context.Context, AccountRecord) error
}

// AccountCurrencyReader projects the two wallet fields needed by GM balance
// views, without fetching the account's unrelated metadata and permanent receipts.
type AccountCurrencyReader interface {
	LoadAccountCurrency(context.Context, string) (AccountRecord, bool, error)
}

type CharacterManagementUnitOfWork interface {
	WithinCharacterManagement(context.Context, string, string, func(Group, ManagementReceiptStore) error) error
	CharacterManagementLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}

// CharacterEquipmentSlotWriter updates only the unlock bitset inside a fenced
// character management transaction. It never saves the aggregate Stats map.
type CharacterEquipmentSlotWriter interface {
	UnlockEquipmentSlots(context.Context, string, byte) error
}

// Gold management has its own durable receipt namespace. Reads select the
// authoritative wallet columns without loading the character's other records.
type CharacterGoldReader interface {
	LoadCharacterGold(context.Context, string) (CharacterRecord, bool, error)
}

type CharacterGoldRepository interface {
	CharacterGoldReader
	SaveCharacterGold(context.Context, CharacterRecord) error
}

type CharacterGoldUnitOfWork interface {
	WithinCharacterGold(context.Context, string, string, func(CharacterGoldRepository, ManagementReceiptStore) error) error
	CharacterGoldLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}

// Character and AccountInventory implement this contract for their own storage
// and receipts. The log key is the corresponding character or account ID.
type InventoryManagementUnitOfWork interface {
	WithinInventoryManagement(context.Context, string, string, func(Group, ManagementReceiptStore) error) error
	InventoryManagementLog(context.Context, string, int64, int) ([]ManagementLogRow, error)
}

type InventoryReceiptReader interface {
	LookupInventoryReceipt(context.Context, string, string, string) ([]byte, bool, error)
}

type InventoryManagementAccountCharacters interface {
	ManagementAccountCharacters(context.Context, string) ([]CharacterSummary, error)
}
