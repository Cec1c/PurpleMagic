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

func (s *Server) scanBenefitDeliveries(ctx context.Context, position benefitScanPosition) (benefitScanPosition, error) {
	if s.deps.Benefits == nil {
		return position, errors.New("benefit store unavailable")
	}
	campaigns, err := s.deps.Benefits.Campaigns(ctx)
	if err != nil {
		return position, err
	}
	sort.Slice(campaigns, func(i, j int) bool { return campaigns[i].ID < campaigns[j].ID })
	active := false
	for _, c := range campaigns {
		active = active || (c.Enabled && (c.AutoExistingCharacters || c.AutoNewCharacters))
	}
	if !active {
		return benefitScanPosition{}, nil
	}
	group, err := s.repositories()
	if err != nil {
		return position, err
	}
	op := ">"
	if position.Character != "" {
		op = ">="
	}
	rows, err := diagnostics.Query(ctx, s.db, "scan.registration", "SELECT id,account_id FROM "+quoteIdentifier(s.cfg.Table)+" WHERE id"+op+"? ORDER BY id LIMIT 5", position.Account)
	if err != nil {
		return position, err
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
			return position, err
		}
		accounts = append(accounts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return position, err
	}
	var failures []error
	attempts := 0
	for _, a := range accounts {
		if err = ctx.Err(); err != nil {
			return position, errors.Join(append(failures, err)...)
		}
		characters, e := testbenefits.CharactersForAccount(ctx, group, a.key)
		account, found, loadErr := group.Account.Load(ctx, a.key)
		if e != nil || loadErr != nil || !found {
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
				// Check receipts before entering expensive locking transactions. The write
				// path repeats this check under its original account lock.
				if accountSent[c.ID] {
					continue
				}
				receipt, e := testbenefits.PreviousDeliveryReceipt(account, c.Reward, testbenefits.Recipient{AccountID: ch.AccountID, CharacterID: ch.CharacterID, CreatedAt: ch.CreatedAt})
				if e == nil && receipt.MailID != "" {
					continue
				}
				if attempts >= 10 {
					return position, errors.Join(failures...)
				}
				if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 4*time.Second {
					return position, errors.Join(failures...)
				}
				if e == nil {
					delivery, stop := context.WithTimeout(ctx, 4*time.Second)
					e = s.deps.Benefits.DeliverCampaignToNewCharacter(delivery, group, c.ID, ch, s.v1ProjectCampaignDelivery)
					stop()
				}
				attempts++
				position = benefitScanPosition{Account: a.id, Character: ch.CharacterID, Campaign: c.ID}
				if e != nil {
					failures = append(failures, e)
					if s.cfg.Logger != nil {
						diagnostics.Log(s.cfg.Logger, ctx, "benefit-delivery-retry", e, "account_cursor", a.id, "character_id", ch.CharacterID, "campaign_id", c.ID)
					}
				} else if c.Reward.Scope.Effective() == testbenefits.ScopeAccount {
					accountSent[c.ID] = true
				}
			}
		}
		position = benefitScanPosition{Account: a.id}
	}
	if len(accounts) < 5 {
		position = benefitScanPosition{}
	}
	return position, errors.Join(failures...)
}
