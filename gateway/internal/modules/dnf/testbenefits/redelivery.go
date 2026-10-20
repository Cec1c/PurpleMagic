package testbenefits

import (
	"context"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// RedeliverCampaignRecipient shares the automatic delivery identity and rule
// lock. A paused or revised rule cannot continue an older confirmed batch.
func (s *Store) RedeliverCampaignRecipient(ctx context.Context, group repository.Group, id string, revision uint64, rec Recipient, project RewardProjector) (DeliveryReceipt, error) {
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
	return receipt, tx.Commit()
}
