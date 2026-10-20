package repository

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// SystemMailAppender is available only on transaction-scoped stores.
// It allocates the ID under the mailbox owner lock and preserves existing mail.
type SystemMailAppender interface {
	AppendSystemMailRecord(context.Context, string, MailRecord) (string, error)
}

var ErrSystemMailInvalid = errors.New("system mailbox delivery is invalid")

// SystemMailDelivery is a server-owned, non-expiring mail created by a
// committed gameplay transaction. It deliberately has no player sender or
// postage; callers must supply transaction-scoped MailboxRepository instances
// so reward consumption and the durable mail commit together.
type SystemMailDelivery struct {
	RecipientCharacterID string
	Title                string
	Body                 string
	Source               string
	Attachments          []MailAttachment
	Gold                 int64
	CreatedAt            time.Time
}

// AppendSystemMail appends one non-expiring system mail to a character's
// mailbox. It is safe to call only through the transaction supplied by the
// relevant asset owner; this helper owns no transaction itself.
func AppendSystemMail(ctx context.Context, repo MailboxRepository, delivery SystemMailDelivery) (string, error) {
	return appendSystemMail(ctx, repo, delivery, false)
}

// AppendSystemNotification records a text-only notice for an already credited
// account reward. The caller commits the wallet and notice in one transaction.
func AppendSystemNotification(ctx context.Context, repo MailboxRepository, delivery SystemMailDelivery) (string, error) {
	if delivery.Gold != 0 || len(delivery.Attachments) != 0 {
		return "", ErrSystemMailInvalid
	}
	return appendSystemMail(ctx, repo, delivery, true)
}

func appendSystemMail(ctx context.Context, repo MailboxRepository, delivery SystemMailDelivery, notification bool) (string, error) {
	recipientID := strings.TrimSpace(delivery.RecipientCharacterID)
	if repo == nil || recipientID == "" || strings.TrimSpace(delivery.Source) == "" || (!notification && len(delivery.Attachments) == 0 && delivery.Gold == 0) || delivery.Gold < 0 || delivery.Gold > math.MaxInt32 {
		return "", ErrSystemMailInvalid
	}
	attachments := cloneMailAttachments(delivery.Attachments)
	for _, attachment := range attachments {
		if attachment.ItemID <= 0 || attachment.Count <= 0 {
			return "", ErrSystemMailInvalid
		}
	}

	now := delivery.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	mail := MailRecord{
		SenderCharacterID:    "0",
		SenderName:           "系统",
		RecipientCharacterID: recipientID,
		Title:                strings.TrimSpace(delivery.Title),
		Body:                 strings.TrimSpace(delivery.Body),
		Attachments:          attachments,
		Gold:                 delivery.Gold,
		CreatedAt:            now,
		// A system overflow delivery must remain claimable until the player
		// makes room. A zero expiry is the repository's durable non-expiring
		// representation and is projected as such by the mailbox owner.
		Metadata: map[string]string{
			"source":      strings.TrimSpace(delivery.Source),
			"system_mail": "true",
			"unlimited":   "true",
		},
	}
	if appender, ok := repo.(SystemMailAppender); ok {
		return appender.AppendSystemMailRecord(ctx, recipientID, mail)
	}
	record, found, err := repo.Load(ctx, recipientID)
	if err != nil {
		return "", err
	}
	if !found {
		record = MailboxRecord{CharacterID: recipientID}
	}
	if record.Mails == nil {
		record.Mails = make(map[string]MailRecord)
	}
	mailID, err := nextSystemMailID(record.Mails)
	if err != nil {
		return "", err
	}
	mail.MailID = mailID
	record.Mails[mailID] = mail
	record.UpdatedAt = now
	if err := SaveMailboxFields(ctx, repo, record, MailboxFieldMails); err != nil {
		return "", err
	}
	return mailID, nil
}

func nextSystemMailID(mails map[string]MailRecord) (string, error) {
	var max uint64
	for key := range mails {
		value, err := strconv.ParseUint(strings.TrimSpace(key), 10, 32)
		if err != nil {
			continue
		}
		if value > max {
			max = value
		}
	}
	if max >= math.MaxUint32 {
		return "", ErrSystemMailInvalid
	}
	return strconv.FormatUint(max+1, 10), nil
}
