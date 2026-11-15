package adminops_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/premium"
	"dnf90.local/gateway/internal/modules/dnf/raid"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type testQuotaUnitOfWork struct {
	state    repository.DungeonQuotaManagementState
	receipts map[string][]byte
}

func (u *testQuotaUnitOfWork) LoadDungeonQuotaManagement(_ context.Context, id string) (repository.DungeonQuotaManagementState, error) {
	if u.state.Character.CharacterID != id {
		return repository.DungeonQuotaManagementState{}, os.ErrNotExist
	}
	return repository.DungeonQuotaManagementState{Character: repository.CloneCharacter(u.state.Character)}, nil
}

func (u *testQuotaUnitOfWork) ResetDungeonQuotaKeys(_ context.Context, id string, deleteKeys []string, sequence int64, updatedAt time.Time) error {
	if u.state.Character.CharacterID != id {
		return os.ErrNotExist
	}
	next := repository.CloneCharacter(u.state.Character)
	for _, k := range deleteKeys {
		delete(next.Stats, k)
	}
	next.Stats["gm_dungeon_quota_sequence_v1"] = sequence
	next.UpdatedAt = updatedAt
	u.state.Character = next
	return nil
}

func (u *testQuotaUnitOfWork) Load(_ context.Context, key string) ([]byte, bool, error) {
	raw, ok := u.receipts[key]
	return slices.Clone(raw), ok, nil
}

func (u *testQuotaUnitOfWork) Append(_ context.Context, key string, payload []byte) error {
	if _, exists := u.receipts[key]; exists {
		return adminops.ErrIdempotency
	}
	u.receipts[key] = slices.Clone(payload)
	return nil
}

func (u *testQuotaUnitOfWork) WithinDungeonQuotaManagement(_ context.Context, _, _ string, fn func(repository.DungeonQuotaManagementRepository, repository.ManagementReceiptStore) error) error {
	snapChar := repository.CloneCharacter(u.state.Character)
	snapReceipts := map[string][]byte{}
	for k, v := range u.receipts {
		snapReceipts[k] = slices.Clone(v)
	}
	if err := fn(u, u); err != nil {
		u.state.Character = snapChar
		u.receipts = snapReceipts
		return err
	}
	return nil
}

func (u *testQuotaUnitOfWork) DungeonQuotaManagementLog(_ context.Context, _ string, _ int64, _ int) ([]repository.ManagementLogRow, error) {
	return nil, nil
}

func testQuotaCatalog() adminops.DungeonQuotaCatalog {
	return adminops.DungeonQuotaCatalog{
		PVFChecksum:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Version:          1,
		EchonTempleIDs:   []int64{3570, 5097},
		EchonTempleLimit: 3,
		EchonTrialIDs:    []int64{3571, 5098},
		EchonTrialLimit:  1,
		AntonWeeklyLimit: 2,
		LukeWeeklyLimit:  2,
		FiendWeeklyLimit: 2,
	}
}

func TestDungeonQuotaRevisionIgnoresOnlineUpdatedAtAndDetectsQuotaDrift(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	day := premium.ServiceDayID(now)
	policy, err := adminops.DefaultDungeonQuotaWeeklyPolicy()
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := raid.WeeklyEpoch(now, policy)
	if err != nil {
		t.Fatal(err)
	}
	cat := testQuotaCatalog()
	resolve := func(context.Context) (adminops.DungeonQuotaCatalog, error) { return cat, nil }
	allowOnline := func(context.Context, string) error { return nil }

	uow := &testQuotaUnitOfWork{
		receipts: map[string][]byte{},
		state: repository.DungeonQuotaManagementState{
			Character: repository.CharacterRecord{
				CharacterID: "10",
				AccountID:   "dnf:1001",
				Level:       90,
				UpdatedAt:   now.Add(-10 * time.Minute),
				Stats: map[string]int64{
					"fatigue":                      120,
					"gold":                         500000,
					"dungeon_daily_entry_316_day":  day,
					"dungeon_daily_entry_316_used": 3,
					"raid_weekly_epoch_v1_2":       epoch,
					"raid_weekly_clears_v1_2":      1,
				},
			},
		},
	}

	revBefore := adminops.DungeonQuotaRevision(uow.state, cat, now, policy)
	cmd := adminops.DungeonQuotaCommand{
		AccountID:   "dnf:1001",
		CharacterID: "10",
		Action:      "reset",
		QuotaKey:    "pandemonium_rift",
		Revision:    revBefore,
		PVFChecksum: cat.PVFChecksum,
		Reason:      "在线角色重置魔界裂缝",
		Actor:       "管理员",
		Key:         "idem-online-rift-reset-0001",
	}

	preview, err := adminops.RunDungeonQuotaOperation(ctx, uow, cmd, false, now, policy, resolve, allowOnline)
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	cmd.DefinitionRevision = preview.DefinitionRevision

	// Simulate go-server periodic online save advancing UpdatedAt and mutating unrelated stats.
	uow.state.Character.UpdatedAt = now.Add(5 * time.Second)
	uow.state.Character.Stats["fatigue"] = 112
	uow.state.Character.Stats["gold"] = 512345

	if got := adminops.DungeonQuotaRevision(uow.state, cat, now, policy); got != revBefore {
		t.Fatalf("UpdatedAt / non-quota stats mutated revision: got %s want %s", got, revBefore)
	}

	committed, err := adminops.RunDungeonQuotaOperation(ctx, uow, cmd, true, now.Add(6*time.Second), policy, resolve, allowOnline)
	if err != nil {
		t.Fatalf("commit after online UpdatedAt advance failed: %v", err)
	}
	if committed.After.Used != 0 || committed.Revision == revBefore {
		t.Fatalf("unexpected commit receipt: %+v", committed)
	}

	// Verify managed stat key changes and gm_dungeon_quota_sequence_v1 changes still invalidate revision.
	for _, tc := range []struct {
		name   string
		mutate func(*repository.CharacterRecord)
	}{
		{
			name: "daily_entry_stat_changed",
			mutate: func(c *repository.CharacterRecord) {
				c.Stats["dungeon_daily_entry_316_day"] = day
				c.Stats["dungeon_daily_entry_316_used"] = 1
			},
		},
		{
			name: "raid_weekly_stat_changed",
			mutate: func(c *repository.CharacterRecord) {
				c.Stats["raid_weekly_clears_v1_2"] = 2
			},
		},
		{
			name: "gm_quota_sequence_changed",
			mutate: func(c *repository.CharacterRecord) {
				c.Stats["gm_dungeon_quota_sequence_v1"]++
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := repository.CloneCharacter(uow.state.Character)
			defer func() { uow.state.Character = snap }()

			baseRev := adminops.DungeonQuotaRevision(uow.state, cat, now, policy)
			nextCmd := adminops.DungeonQuotaCommand{
				AccountID:   "dnf:1001",
				CharacterID: "10",
				Action:      "reset",
				QuotaKey:    "luke_raid",
				Revision:    baseRev,
				PVFChecksum: cat.PVFChecksum,
				Reason:      "冲突检测验证",
				Actor:       "管理员",
				Key:         "idem-conflict-" + tc.name,
			}
			p, err := adminops.RunDungeonQuotaOperation(ctx, uow, nextCmd, false, now, policy, resolve, allowOnline)
			if err != nil {
				t.Fatalf("preview failed: %v", err)
			}
			nextCmd.DefinitionRevision = p.DefinitionRevision

			tc.mutate(&uow.state.Character)
			if got := adminops.DungeonQuotaRevision(uow.state, cat, now, policy); got == baseRev {
				t.Fatalf("expected revision to change on %s", tc.name)
			}
			if _, err := adminops.RunDungeonQuotaOperation(ctx, uow, nextCmd, true, now, policy, resolve, allowOnline); !errors.Is(err, adminops.ErrConflict) {
				t.Fatalf("expected ErrConflict on %s, got %v", tc.name, err)
			}
		})
	}
}
