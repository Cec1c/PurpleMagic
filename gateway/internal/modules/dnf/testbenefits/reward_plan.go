package testbenefits

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// RewardPlan retains the approved intent. Instance-dependent values are
// materialized inside the recipient's mailbox transaction at delivery time.
type RewardPlan struct {
	Version int         `json:"version"`
	Options MailOptions `json:"options"`
}

type RewardProjector func(Job, time.Time) ([]repository.MailAttachment, error)

var ErrRewardProjection = errors.New("福利附件需要匹配当前PVF的发放器")

func (j Job) validateRewardPlan() error {
	if j.ReceiptBasis != nil {
		b := j.ReceiptBasis
		hash, err := hex.DecodeString(b.PVFChecksum)
		if j.RewardPlan == nil || err != nil || len(hash) != 32 || len(b.Attachments) != 1 || len(j.Attachments) != 1 || b.Attachments[0].ItemID != j.Attachments[0].ItemID || b.Attachments[0].Count != j.Attachments[0].Count {
			return ErrRewardProjection
		}
	}
	if j.RewardPlan == nil {
		return nil
	}
	p := j.RewardPlan
	digest, err := hex.DecodeString(j.PVFChecksum)
	o := p.Options
	if p.Version != 1 || err != nil || len(digest) != 32 || len(j.Attachments) != 1 ||
		o.ExpireDays < 0 || o.ExpireDays > 3650 || o.Reinforce < 0 || o.Reinforce > 31 || o.AmplifyType < 0 || o.AmplifyType > 4 || o.ForgingLevel < 0 {
		return ErrRewardProjection
	}
	switch o.EquipmentQuality {
	case "", "default", "random", "top":
	default:
		return ErrRewardProjection
	}
	if !j.Attachments[0].ExpireAt.IsZero() {
		return ErrRewardProjection
	}
	return nil
}

func (s *Store) CreateCampaignDraftOnce(ctx context.Context, key string, scope GrantScope, automatic bool, draft MailDraft, project func(MailDraft) (Job, string, error)) (Campaign, error) {
	if len(draft.Recipients) != 0 || project == nil || (scope != ScopeCharacter && scope != ScopeAccount) {
		return Campaign{}, ErrRewardProjection
	}
	input := struct {
		Kind      string
		Scope     GrantScope
		Automatic bool
		Draft     MailDraft
	}{"campaign-plan-v1", scope, automatic, draft}
	raw, err := s.createOnce(ctx, "campaigns", key, input, func(id, fp string) (GrantScope, any, error) {
		reward, name, err := project(draft)
		if err != nil {
			return "", nil, err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		reward.ID, reward.CampaignID, reward.Scope, reward.CreatedAt = id, id, scope, now
		if reward.RewardPlan == nil {
			return "", nil, ErrRewardProjection
		}
		if err = reward.Validate(); err != nil {
			return "", nil, err
		}
		return scope, Campaign{ID: id, RequestFingerprint: fp, CreatedAt: now, AutoNewCharacters: automatic, ItemName: name, Reward: reward}, nil
	})
	var c Campaign
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	return c, err
}

// Separate tables prevent old game/GM readers from treating a dynamic reward
// plan as a completed static attachment. Legacy records keep their tables.
func (s *Store) payloadTables(kind string) []string {
	return []string{s.scopedTable(kind, ScopeCharacter), s.scopedTable(kind, ScopeAccount), s.prefix + "_benefit_v2_" + kind, s.prefix + "_benefit_v3_" + kind}
}

func (s *Store) recordTable(kind string, scope GrantScope, raw []byte) string {
	var record struct {
		Cera       int64       `json:"cera"`
		RewardPlan *RewardPlan `json:"rewardPlan"`
		Reward     Job         `json:"reward"`
	}
	// raw was just marshaled by the owning operation.
	_ = json.Unmarshal(raw, &record)
	// Currency rules stay invisible to older scanners that only deliver mail.
	if record.Cera != 0 || record.Reward.Cera != 0 {
		return s.prefix + "_benefit_v3_" + kind
	}
	if record.RewardPlan != nil || record.Reward.RewardPlan != nil {
		return s.prefix + "_benefit_v2_" + kind
	}
	return s.scopedTable(kind, scope)
}
