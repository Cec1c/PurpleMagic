package testbenefits

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Selection needs only identity, ownership and incarnation. The delivery
// transaction still reloads and locks the authoritative character and account.
func CharactersForAccount(ctx context.Context, group repository.Group, account string) ([]repository.CharacterRecord, error) {
	var rows []repository.CharacterRecord
	if reader, ok := group.Character.(repository.CharacterSummaryReader); ok {
		summaries, err := reader.SummariesForAccounts(ctx, []string{account})
		if err != nil {
			return nil, err
		}
		for _, c := range summaries {
			rows = append(rows, repository.CharacterRecord{CharacterID: c.CharacterID, AccountID: c.AccountID, Name: c.Name, Slot: c.Slot, CreatedAt: c.CreatedAt})
		}
	} else {
		var err error
		rows, err = group.Character.ListByAccount(ctx, account, repository.DefaultCharacterSlotLimit+1)
		if err != nil {
			return nil, err
		}
	}
	if len(rows) > repository.DefaultCharacterSlotLimit {
		return nil, repository.ErrCharacterSummaryRange
	}
	for _, c := range rows {
		if c.AccountID != account {
			return nil, repository.ErrCharacterSummaryRange
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CharacterID < rows[j].CharacterID })
	return rows, nil
}

type CampaignRecipient struct {
	Recipient
	Eligible bool
}

// CampaignRecipientPage reads live permanent receipts, without creating a job,
// taking a policy write lock or freezing the entire server's recipients.
// A page visits at most five accounts; sparse pages still carry a next cursor.
func (s *Store) CampaignRecipientPage(ctx context.Context, group repository.Group, c Campaign, after string, limit int) ([]CampaignRecipient, string, error) {
	var accountAfter int64
	characterAfter := ""
	if after != "" {
		parts := strings.SplitN(after, ":", 2)
		var err error
		accountAfter, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || accountAfter < 1 || len(parts) != 2 {
			return nil, "", errors.New("invalid benefit recipient cursor")
		}
		characterAfter = parts[1]
	}
	if limit < 1 || limit > 100 {
		return nil, "", ErrPreviewLimit
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,account_id FROM "+s.prefix+" WHERE id>=? ORDER BY id LIMIT 6", accountAfter)
	if err != nil {
		return nil, "", err
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
			return nil, "", err
		}
		accounts = append(accounts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	out := []CampaignRecipient{}
	for _, a := range accounts[:min(5, len(accounts))] {
		characters, err := CharactersForAccount(ctx, group, a.key)
		if err != nil {
			return nil, "", err
		}
		account, found, err := group.Account.Load(ctx, a.key)
		if err != nil {
			return nil, "", err
		}
		if !found {
			continue
		}
		audience := c
		audience.Enabled = true
		allCharacters := characters
		if c.Reward.Scope.Effective() == ScopeAccount && len(characters) > 0 {
			target := characters[0]
			for _, ch := range characters {
				if EligibleNewCharacter(audience, ch) {
					target = ch
					break
				}
			}
			characters = []repository.CharacterRecord{target}
		}
		for _, ch := range characters {
			if ch.Stats["delete_flag"] != 0 || (a.id == accountAfter && ch.CharacterID <= characterAfter) {
				continue
			}
			if len(out) == limit {
				return out, fmt.Sprintf("%d:%s", a.id, characterAfter), nil
			}
			rec := Recipient{AccountID: a.key, CharacterID: ch.CharacterID, Name: ch.Name, CreatedAt: ch.CreatedAt}
			receipt, e := PreviousDeliveryReceipt(account, c.Reward, rec)
			if e != nil {
				rec.Error = e.Error()
			} else if receipt.MailID != "" {
				rec.MailID = receipt.MailID
				rec.DeliveredCharacterID = receipt.CharacterID
				if c.Reward.Scope.Effective() == ScopeAccount {
					rec.CharacterID, rec.Name = receipt.CharacterID, "已删除角色"
					for _, delivered := range allCharacters {
						if delivered.CharacterID == receipt.CharacterID {
							rec.Name = delivered.Name
							break
						}
					}
				}
				if c.Reward.RewardPlan != nil || c.Reward.Cera > 0 {
					rec.Delivery = &receipt
				}
			}
			out = append(out, CampaignRecipient{Recipient: rec, Eligible: EligibleNewCharacter(audience, ch)})
			characterAfter = ch.CharacterID
		}
		characterAfter = ""
	}
	if len(accounts) > 5 {
		return out, fmt.Sprintf("%d:", accounts[5].id), nil
	}
	return out, "", nil
}
