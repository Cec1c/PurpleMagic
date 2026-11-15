package repository

import (
	"context"
	"errors"
)

var ErrResourceRange = errors.New("invalid character resource range")

// CharacterResourceReader reads bounded projections without loading mutable
// domain aggregates. The caller supplies a freshly resolved character summary.
type CharacterResourceReader interface {
	InventoryPage(context.Context, CharacterSummary, string, int, int, bool) ([]InventorySlot, error)
	MailboxPage(context.Context, string, string, int) ([]MailRecord, error)
	MailDetail(context.Context, string, string) (MailRecord, bool, error)
	QuestPage(context.Context, string, string, int64, int) ([]CharacterQuest, error)
}

// CharacterBatchInventoryReader reads multiple inventory containers in merged
// shard queries without loading raw bytes or extra fields.
type CharacterBatchInventoryReader interface {
	InventoryBatch(ctx context.Context, c CharacterSummary, containers []string, limit int) (map[string][]InventorySlot, error)
}

type InventorySlot struct {
	Key   string
	Slot  int
	Stack ItemStack
}

type CharacterQuest struct {
	ID    int64
	State QuestState
}

// ContainerSpec follows the current inventory owner's list/slot layout. Account
// cargo and shared cubes/souls are owned by AccountInventory; worn items by Equipment.
type ContainerSpec struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	Collection string `json:"-"`
	List       int    `json:"-"`
	First      int    `json:"-"`
	Last       int    `json:"-"`
}

var resourceContainers = []ContainerSpec{
	{"equipment", "背包装备", "character", "slots", 0, 9, 64},
	{"consumables", "消耗品", "character", "slots", 0, 65, 120},
	{"materials", "材料", "character", "slots", 0, 121, 176},
	{"quest-items", "任务物品", "character", "slots", 0, 177, 232},
	{"profession", "副职业材料", "character", "slots", 0, 233, 288},
	{"quickbar", "快捷栏", "character", "slots", 0, 3, 8},
	{"avatars", "装扮", "character", "slots", 1, 0, 32767},
	{"pets", "宠物", "character", "slots", 7, 0, 139},
	{"pet-equipment", "宠物装备", "character", "slots", 7, 140, 188},
	{"pet-consumables", "宠物消耗品", "character", "slots", 7, 189, 238},
	{"medals", "勋章与守护珠", "character", "slots", 38, 0, 32767},
	{"equipped", "身上穿戴", "equipment", "", 3, 0, 32767},
	{"warehouse", "个人仓库", "character", "warehouse", 2, 0, 32767},
	{"account-cargo", "账号金库", "account", "", 12, 0, 32767},
	{"shared", "账号晶块与灵魂", "account", "", 0, 354, 365},
}

func ResourceContainers() []ContainerSpec { return append([]ContainerSpec(nil), resourceContainers...) }
func ResourceContainer(id string) (ContainerSpec, bool) {
	for _, c := range resourceContainers {
		if c.ID == id {
			return c, true
		}
	}
	return ContainerSpec{}, false
}
