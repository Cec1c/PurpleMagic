package gmtool

import (
	"context"
	"errors"
	"sort"
	"time"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

// Account is the last completed account unless Character is nonempty, in
// which case Character/Campaign identify the last attempted delivery within it.
// Cursor state is disposable; only transactional receipts prove delivery.
type benefitScanPosition struct {
	Account   int64  `json:"account"`
	Character string `json:"character,omitempty"`
	Campaign  string `json:"campaign,omitempty"`
}

type benefitScanResult struct {
	Position  benefitScanPosition `json:"position"`
	Retry     bool                `json:"retry"`
	Done      bool                `json:"done"`
	Checked   int                 `json:"checked"`
	Delivered int                 `json:"delivered"`
	Received  int                 `json:"received"`
	Failed    int                 `json:"failed"`
	Blocked   []benefitScanBlock  `json:"blocked,omitempty"`
}
type benefitScanBlock struct {
	CampaignID string `json:"campaignId"`
	Message    string `json:"message"`
}

func (s *Server) scanBenefitDeliveries(ctx context.Context, position benefitScanPosition) (benefitScanPosition, error) {
	out, err := s.scanBenefitPage(ctx, position)
	return out.Position, err
}

func (s *Server) scanBenefitPage(ctx context.Context, position benefitScanPosition) (out benefitScanResult, resultErr error) {
	defer func() { out.Position = position; out.Retry = resultErr != nil }()
	if s.deps.Benefits == nil {
		return out, errors.New("benefit store unavailable")
	}
	campaigns, err := s.deps.Benefits.Campaigns(ctx)
	if err != nil {
		return out, err
	}
	sort.Slice(campaigns, func(i, j int) bool { return campaigns[i].ID < campaigns[j].ID })
	active := false
	blocked := map[string]bool{}
	for i, c := range campaigns {
		active = active || (c.Enabled && (c.AutoExistingCharacters || c.AutoNewCharacters))
		if c.Enabled && (c.AutoExistingCharacters || c.AutoNewCharacters) && c.Reward.PVFChecksum != "" && c.Reward.PVFChecksum != s.v1PVFChecksum() {
			updated, e := s.reconcileCampaignPVF(ctx, c)
			if e == nil {
				campaigns[i] = updated
			} else {
				blocked[c.ID] = true
				out.Blocked = append(out.Blocked, benefitScanBlock{c.ID, "物品版本待核对，未发放对象保留待发"})
				diagnostics.Log(s.cfg.Logger, ctx, "benefit-pvf-review-required", e, "campaign_id", c.ID)
			}
		}
	}
	if !active {
		position = benefitScanPosition{}
		out.Done = true
		return out, nil
	}
	group, err := s.repositories()
	if err != nil {
		return out, err
	}
	op := ">"
	if position.Character != "" {
		op = ">="
	}
	rows, err := diagnostics.Query(ctx, s.db, "scan.registration", "SELECT id,account_id FROM "+quoteIdentifier(s.cfg.Table)+" WHERE id"+op+"? ORDER BY id LIMIT 5", position.Account)
	if err != nil {
		return out, err
	}
	type account struct {
		id  int64
		key string
	}
	var accounts []account
	for rows.Next() {
		var a account
		if err = rows.Scan(&a.id, &a.key); err != nil {
			rows.Close()
			return out, err
		}
		accounts = append(accounts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var failures []error
	attempts := 0
	for _, a := range accounts {
		if err = ctx.Err(); err != nil {
			return out, errors.Join(append(failures, err)...)
		}
		characters, e := testbenefits.CharactersForAccount(ctx, group, a.key)
		if e == nil && len(characters) == 0 {
			position = benefitScanPosition{Account: a.id}
			continue
		}
		account, found, loadErr := group.Account.Load(ctx, a.key)
		if e != nil || loadErr != nil || !found {
			if e == nil && loadErr == nil {
				loadErr = errors.New("benefit account unavailable")
			}
			out.Failed++
			failures = append(failures, errors.Join(e, loadErr))
			position = benefitScanPosition{Account: a.id}
			continue
		}
		accountSent := map[string]bool{}
		for _, ch := range characters {
			for _, c := range campaigns {
				if a.id == position.Account && position.Character != "" && (ch.CharacterID < position.Character || (ch.CharacterID == position.Character && c.ID <= position.Campaign)) {
					continue
				}
				if !testbenefits.EligibleNewCharacter(c, ch) {
					continue
				}
				if blocked[c.ID] {
					continue
				}
				// Check receipts before entering expensive locking transactions. The write
				// path repeats this check under its original account lock.
				if accountSent[c.ID] {
					continue
				}
				receipt, e := testbenefits.PreviousDeliveryReceipt(account, c.Reward, testbenefits.Recipient{AccountID: ch.AccountID, CharacterID: ch.CharacterID, CreatedAt: ch.CreatedAt})
				if e == nil && receipt.MailID != "" {
					out.Checked++
					out.Received++
					position = benefitScanPosition{Account: a.id, Character: ch.CharacterID, Campaign: c.ID}
					if c.Reward.Scope.Effective() == testbenefits.ScopeAccount {
						accountSent[c.ID] = true
					}
					continue
				}
				if attempts >= 10 {
					return out, errors.Join(failures...)
				}
				if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 4*time.Second {
					return out, errors.Join(failures...)
				}
				if e == nil {
					delivery, stop := context.WithTimeout(ctx, 4*time.Second)
					e = s.deps.Benefits.DeliverCampaignToNewCharacter(delivery, group, c.ID, ch, s.v1ProjectCampaignDelivery)
					stop()
				}
				attempts++
				out.Checked++
				position = benefitScanPosition{Account: a.id, Character: ch.CharacterID, Campaign: c.ID}
				if e != nil {
					out.Failed++
					failures = append(failures, e)
					if s.cfg.Logger != nil {
						diagnostics.Log(s.cfg.Logger, ctx, "benefit-delivery-retry", e, "account_cursor", a.id, "character_id", ch.CharacterID, "campaign_id", c.ID)
					}
				} else {
					out.Delivered++
					if c.Reward.Scope.Effective() == testbenefits.ScopeAccount {
						accountSent[c.ID] = true
					}
				}
			}
		}
		position = benefitScanPosition{Account: a.id}
	}
	if len(accounts) < 5 {
		position = benefitScanPosition{}
		out.Done = true
	}
	return out, errors.Join(failures...)
}
