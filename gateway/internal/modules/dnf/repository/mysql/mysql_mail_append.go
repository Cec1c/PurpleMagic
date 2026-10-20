package mysql

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Only exposed by WithinAccountMailboxCharacter, which already holds the
// character/account locks. All appends, wallet changes and receipts use its tx.
type mailDeliveryMailboxRepository struct {
	mailboxAssetScopedMailboxRepository
	router mysqlRouter
}

func (s *mailDeliveryMailboxRepository) AppendSystemMailRecord(ctx context.Context, id string, mail repository.MailRecord) (string, error) {
	if err := repository.TransactionKeyError(ctx, s.characterID, id); err != nil {
		return "", err
	}
	tables := []string{}
	for _, name := range []string{mysqlMailboxTable, mysqlMailsTable, mysqlMailMetadataTable, mysqlMailAttachmentsTable, mysqlMailAttachmentExtraTable} {
		table, err := s.router.writeTable(name, id)
		if err != nil {
			return "", err
		}
		tables = append(tables, table)
	}
	// Upsert acquires the same parent-row lock as gameplay's aggregate Save,
	// including when a character has never had a mailbox.
	_, err := s.router.db.ExecContext(ctx, "INSERT INTO "+tables[0]+" (character_id,updated_at) VALUES (?,?) ON DUPLICATE KEY UPDATE updated_at=updated_at", id, mail.CreatedAt)
	if err != nil {
		return "", err
	}
	rows, err := s.router.db.QueryContext(ctx, "SELECT mail_id FROM "+tables[1]+" WHERE character_id=? FOR UPDATE", id)
	if err != nil {
		return "", err
	}
	var max uint64
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return "", err
		}
		value, e := strconv.ParseUint(strings.TrimSpace(key), 10, 32)
		if e == nil && value > max {
			max = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if max >= math.MaxUint32 {
		return "", repository.ErrSystemMailInvalid
	}
	mail.MailID = strconv.FormatUint(max+1, 10)
	if err = insertMails(ctx, s.router.db, tables[1], tables[2], tables[3], tables[4], id, map[string]repository.MailRecord{mail.MailID: mail}); err != nil {
		return "", err
	}
	_, err = s.router.db.ExecContext(ctx, "UPDATE "+tables[0]+" SET updated_at=GREATEST(COALESCE(updated_at,?),?)+INTERVAL 1 MICROSECOND WHERE character_id=?", mail.CreatedAt, mail.CreatedAt, id)
	return mail.MailID, err
}

type mailDeliveryAccountRepository struct{ rentalScopedAccountRepository }

func (s *mailDeliveryAccountRepository) SaveMetadataEntry(ctx context.Context, id, key, value string, at time.Time) error {
	if err := repository.TransactionKeyError(ctx, s.accountID, id); err != nil {
		return err
	}
	writer, ok := s.base.(repository.AccountMetadataEntryWriter)
	if !ok {
		return repository.ErrAccountMetadataUnavailable
	}
	return writer.SaveMetadataEntry(ctx, id, key, value, at)
}
