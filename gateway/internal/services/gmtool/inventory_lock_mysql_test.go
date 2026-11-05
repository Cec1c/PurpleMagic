package gmtool

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func TestV1InventoryAccountDoesNotLockCharacterMySQL(t *testing.T) {
	for _, shards := range []int{1, 2} {
		t.Run(fmt.Sprint(shards), func(t *testing.T) {
			s, _, open, names := currencyFixtureDatabases(t, shards)
			ctx := context.Background()
			g := open()
			if err := g.Character.Save(ctx, repository.CharacterRecord{CharacterID: "10", AccountID: "wallet:1", Name: "fixture", Job: "0", Level: 90}); err != nil {
				t.Fatal(err)
			}
			if err := g.AccountInventory.Save(ctx, repository.AccountInventoryRecord{AccountID: "wallet:1", Slots: map[string]repository.ItemStack{"0:354": {ItemID: 3037, Count: 40}}}); err != nil {
				t.Fatal(err)
			}
			game, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer game.Rollback()
			// Reproduce the production lock shape: the game owns the character row
			// while GM owns the account row. A snapshot ownership read must proceed.
			for _, db := range names {
				rows, err := game.QueryContext(ctx, "SELECT character_id FROM `"+db+"`.`dnf_characters` WHERE character_id='10' FOR UPDATE")
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
			bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			u := g.AccountInventory.(repository.InventoryManagementUnitOfWork)
			err = u.WithinInventoryManagement(bounded, "wallet:1", "10", func(scoped repository.Group, receipts repository.ManagementReceiptStore) error {
				ch, found, err := scoped.Character.Load(bounded, "10")
				if err != nil {
					return err
				}
				if !found || ch.AccountID != "wallet:1" {
					return fmt.Errorf("owner missing")
				}
				rows, err := scoped.Character.(repository.InventoryManagementAccountCharacters).ManagementAccountCharacters(bounded, "wallet:1")
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return fmt.Errorf("roles missing")
				}
				return nil
			})
			if err != nil {
				t.Fatal("GM waited for gameplay character lock", err)
			}
			// Receipt recovery is independent of both the character and account lock.
			for _, db := range names {
				rows, err := game.QueryContext(ctx, "SELECT account_id FROM `"+db+"`.`dnf_accounts` WHERE account_id='wallet:1' FOR UPDATE")
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
			for _, reader := range []repository.InventoryReceiptReader{g.Character.(repository.InventoryReceiptReader), g.AccountInventory.(repository.InventoryReceiptReader)} {
				if _, found, err := reader.LookupInventoryReceipt(bounded, "wallet:1", "10", "absent-fixture-operation"); err != nil || found {
					t.Fatal("receipt lookup blocked", found, err)
				}
			}
		})
	}
}
