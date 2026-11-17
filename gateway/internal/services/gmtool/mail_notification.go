package gmtool

import (
	"context"
	"errors"
	"strconv"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

const (
	notifyMailboxAction     = "notify_mailbox"
	mailNotificationTimeout = 3 * time.Second
)

// notifyMailboxReceipts projects already committed mail into live game
// sessions. It is intentionally best effort: a notification failure is logged
// and never turns a successful, receipt-backed mail delivery into a failure.
// Receipt character IDs are authoritative, including account-scoped replays.
func (s *Server) notifyMailboxReceipts(ctx context.Context, receipts []testbenefits.DeliveryReceipt) error {
	return s.notifyMailboxReceiptsWithin(ctx, receipts, mailNotificationTimeout)
}

func (s *Server) notifyMailboxReceiptsWithin(ctx context.Context, receipts []testbenefits.DeliveryReceipt, timeout time.Duration) error {
	if s == nil || s.deps.Runtime == nil || len(receipts) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = mailNotificationTimeout
	}
	// Committed mail still deserves a bounded live projection if the browser
	// disconnected after submitting the request. Context values are retained.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	seen := make(map[string]bool, len(receipts))
	var failures []error
	for _, receipt := range receipts {
		characterID := receipt.CharacterID
		// A receipt without a mail ID does not identify an inbox delivery.
		// Wallet grants with a text notification still have a mail ID and qualify.
		if receipt.MailID == "" || characterID == "" || seen[characterID] {
			continue
		}
		seen[characterID] = true
		n, err := strconv.ParseUint(characterID, 10, 16)
		if err != nil || n == 0 {
			err = errors.New("invalid receipt character id")
		} else {
			var response adminapi.Response
			response, err = s.deps.Runtime.Call(ctx, adminapi.Request{Action: notifyMailboxAction, CharacterID: uint16(n)})
			if err == nil {
				s.logEvent("gm-mailbox-notification",
					"character_id", characterID,
					"mail_id", receipt.MailID,
					"online", response.Online)
				continue
			}
		}
		failures = append(failures, err)
		s.logEvent("gm-mailbox-notification-failed",
			"character_id", characterID,
			"mail_id", receipt.MailID,
			"error", err.Error())
	}
	return errors.Join(failures...)
}

func (s *Server) notifyMailboxReceiptAfterCommit(ctx context.Context, receipt testbenefits.DeliveryReceipt) {
	_ = s.notifyMailboxReceipts(ctx, []testbenefits.DeliveryReceipt{receipt})
}

func (s *Server) notifyMailboxReceiptsAfterCommit(ctx context.Context, receipts []testbenefits.DeliveryReceipt) {
	_ = s.notifyMailboxReceipts(ctx, receipts)
}
