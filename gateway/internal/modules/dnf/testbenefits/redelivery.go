package testbenefits

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// RedeliverCampaignRecipient shares the automatic delivery identity and rule
// lock. A paused or revised rule cannot continue an older confirmed batch.
func (s *Store) RedeliverCampaignRecipient(ctx context.Context, group repository.Group, id string, revision uint64, rec Recipient, project RewardProjector) (DeliveryReceipt, error) {
	return s.RedeliverCampaignRecipientCommitted(ctx, group, id, revision, rec, project, nil)
}

// RedeliverCampaignRecipientCommitted shares the same durable receipt path as
// ordinary campaign delivery. afterCommit runs only after the campaign lock and
// delivery transaction commits, and receives the receipt's actual character.
func (s *Store) RedeliverCampaignRecipientCommitted(ctx context.Context, group repository.Group, id string, revision uint64, rec Recipient, project RewardProjector, afterCommit func(context.Context, DeliveryReceipt)) (DeliveryReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryReceipt{}, err
	}
	defer tx.Rollback()
	c, err := s.lockCampaign(ctx, tx, id)
	if err != nil {
		return DeliveryReceipt{}, err
	}
	if c.Revision != revision {
		return DeliveryReceipt{}, ErrRevision
	}
	if !c.Enabled {
		return DeliveryReceipt{}, ErrCampaignDisabled
	}
	ch := repository.CharacterRecord{AccountID: rec.AccountID, CharacterID: rec.CharacterID, CreatedAt: rec.CreatedAt}
	if !EligibleNewCharacter(c, ch) {
		return DeliveryReceipt{}, ErrRecipient
	}
	receipt, err := DeliverProjectedWithReceipt(ctx, group, c.Reward, rec, project)
	if err != nil {
		return receipt, err
	}
	if err = tx.Commit(); err != nil {
		return DeliveryReceipt{}, err
	}
	if afterCommit != nil {
		afterCommit(ctx, receipt)
	}
	return receipt, nil
}
