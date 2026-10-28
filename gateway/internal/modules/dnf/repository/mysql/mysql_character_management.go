package mysql

import (
	"context"
	"database/sql"
	"errors"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type characterManagementReceipts struct {
	db                        SQLDB
	table, character, account string
}

func (s characterManagementReceipts) Load(ctx context.Context, key string) ([]byte, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM "+s.table+" WHERE character_id=? AND request_id=?", s.character, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return raw, err == nil, err
}
func (s characterManagementReceipts) Append(ctx context.Context, key string, raw []byte) error {
	if len(raw) > 2<<20 || len(key) < 16 || len(key) > 128 {
		return errors.New("invalid character management receipt")
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO "+s.table+" (character_id,account_id,request_id,payload,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))", s.character, s.account, key, raw)
	return err
}

func (s *mysqlCharStore) WithinCharacterManagement(ctx context.Context, accountID, characterID string, apply func(repository.Group, repository.ManagementReceiptStore) error) error {
	return s.withinCharacterManagement(ctx, accountID, characterID, "character_management_operations", apply)
}

func (s *mysqlCharStore) WithinInventoryManagement(ctx context.Context, accountID, characterID string, apply func(repository.Group, repository.ManagementReceiptStore) error) error {
	return s.withinCharacterManagement(ctx, accountID, characterID, "inventory_management_operations", apply)
}

func (s *mysqlCharStore) WithinCharacterGold(ctx context.Context, accountID, characterID string, apply func(repository.CharacterGoldRepository, repository.ManagementReceiptStore) error) error {
	if apply == nil {
		return errors.New("invalid gold operation")
	}
	return s.withinCharacterManagementRouter(ctx, accountID, characterID, "character_gold_operations", func(r mysqlRouter, receipts repository.ManagementReceiptStore) error {
		return apply(&mysqlCharacterGoldStore{base: &mysqlCharStore{mysqlStoreBase: mysqlStoreBase{router: r}}, account: accountID, character: characterID}, receipts)
	})
}

func (s *mysqlCharStore) CharacterGoldLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return s.characterManagementLog(ctx, id, before, limit, "character_gold_operations")
}

func (s *mysqlCharStore) withinCharacterManagement(ctx context.Context, accountID, characterID, receiptTable string, apply func(repository.Group, repository.ManagementReceiptStore) error) error {
	if apply == nil {
		return errors.New("invalid character operation")
	}
	return s.withinCharacterManagementRouter(ctx, accountID, characterID, receiptTable, func(r mysqlRouter, receipts repository.ManagementReceiptStore) error {
		base := newMySQLGroupFromRouter(r, false)
		g := repository.Group{
			Character: &characterSettlementScopedCharacterRepository{characterID: characterID, base: base.Character},
			Quest:     &characterSettlementScopedQuestRepository{characterID: characterID, base: base.Quest},
			Skill:     &characterSettlementScopedSkillRepository{characterID: characterID, base: base.Skill},
			Inventory: &characterSettlementScopedInventoryRepository{characterID: characterID, base: base.Inventory},
			Equipment: &characterSettlementScopedEquipmentRepository{characterID: characterID, base: base.Equipment},
			Pet:       &characterPetScopedPetRepository{characterID: characterID, base: base.Pet},
		}
		if receiptTable == "character_management_operations" {
			g.Character = &managedEquipmentSlotStore{
				characterSettlementScopedCharacterRepository: &characterSettlementScopedCharacterRepository{characterID: characterID, base: base.Character},
				router: r,
			}
			g.Quest = &managedQuestStore{base: &mysqlQuestStore{mysqlStoreBase{router: r}}, character: characterID}
		}

		return apply(g, receipts)
	})
}

type managedEquipmentSlotStore struct {
	*characterSettlementScopedCharacterRepository
	router mysqlRouter
}

func (s *managedEquipmentSlotStore) UnlockEquipmentSlots(ctx context.Context, id string, bits byte) error {
	if err := repository.TransactionKeyError(ctx, s.characterID, id); err != nil {
		return err
	}
	table, err := s.router.writeTable(mysqlCharTable, id)
	if err != nil {
		return err
	}
	_, err = s.router.db.ExecContext(ctx, "UPDATE "+table+" SET ex_equip_slot_stat=ex_equip_slot_stat | ?, updated_at=? WHERE character_id=?", bits, s.router.now(), id)
	return err
}

func (s *mysqlCharStore) withinCharacterManagementRouter(ctx context.Context, accountID, characterID, receiptTable string, apply func(mysqlRouter, repository.ManagementReceiptStore) error) error {
	if accountID == "" || characterID == "" || apply == nil {
		return errors.New("invalid character operation")
	}
	beginner, ok := s.router.db.(sqlTxBeginner)
	if !ok {
		return errors.New("character transaction unavailable")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r := s.router
	r.db = sqlTransactionDB{tx: tx}
	r.readDBs = append([]string(nil), r.writeDBs...)
	r.lockReads = true
	if err = lockManagedAccount(ctx, tx, r, accountID); err != nil {
		return err
	}
	table, err := r.writeTable(mysqlCharTable, characterID)
	if err != nil {
		return err
	}
	var actual string
	err = tx.QueryRowContext(ctx, "SELECT character_id FROM "+table+" WHERE character_id=? AND BINARY character_id=BINARY ? AND BINARY account_id=BINARY ? FOR UPDATE", characterID, characterID, accountID).Scan(&actual)
	if err != nil {
		return err
	}
	table, err = r.writeTable(receiptTable, characterID)
	if err != nil {
		return err
	}
	if err = apply(r, characterManagementReceipts{db: r.db, table: table, character: characterID, account: accountID}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *mysqlCharStore) CharacterManagementLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return s.characterManagementLog(ctx, id, before, limit, "character_management_operations")
}

func (s *mysqlCharStore) InventoryManagementLog(ctx context.Context, id string, before int64, limit int) ([]repository.ManagementLogRow, error) {
	return s.characterManagementLog(ctx, id, before, limit, "inventory_management_operations")
}

func (s *mysqlCharStore) characterManagementLog(ctx context.Context, id string, before int64, limit int, receiptTable string) ([]repository.ManagementLogRow, error) {
	if id == "" || before < 0 || limit < 1 || limit > 101 {
		return nil, errors.New("invalid character management page")
	}
	table, err := s.router.readTable(receiptTable, id)
	if err != nil {
		return nil, err
	}
	q := "SELECT sequence_id,payload FROM " + table + " WHERE character_id=?"
	args := []any{id}
	if before > 0 {
		q += " AND sequence_id<?"
		args = append(args, before)
	}
	q += " ORDER BY sequence_id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.router.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []repository.ManagementLogRow{}
	for rows.Next() {
		var r repository.ManagementLogRow
		if err = rows.Scan(&r.Sequence, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
