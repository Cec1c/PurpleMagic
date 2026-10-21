// Package testbenefits owns optional test-server welcome grants and gold-mail
// campaigns. Zero settings preserve normal gameplay and existing balances.
package testbenefits

import (
	"context"
	"crypto/sha256"
	"dnf90.local/gateway/internal/diagnostics"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const MaxAmount int64 = math.MaxInt32
const WelcomeReceipt = "test_benefits_welcome_cera_v1"

type GrantScope string

const (
	ScopeCharacter GrantScope = "character"
	ScopeAccount   GrantScope = "account"
)

// Empty is the pre-scope format and must retain its exact receipt identity.
func (s GrantScope) Effective() GrantScope {
	if s == "" {
		return ScopeCharacter
	}
	return s
}

type DeliveryReceipt struct {
	Cera        int64                       `json:"cera,omitempty"`
	Before      string                      `json:"before,omitempty"`
	After       string                      `json:"after,omitempty"`
	PVFChecksum string                      `json:"pvfChecksum,omitempty"`
	GrantedAt   time.Time                   `json:"grantedAt,omitempty"`
	Attachments []repository.MailAttachment `json:"attachments,omitempty"`
	MailID      string                      `json:"mailId"`
	CharacterID string                      `json:"characterId"`
	CreatedAt   time.Time                   `json:"characterCreatedAt"`
}

type Policy struct {
	Revision      uint64 `json:"revision"`
	AccountCera   int64  `json:"accountCera"`
	CharacterGold int64  `json:"characterGold"`
}

func (p Policy) Validate() error {
	if p.AccountCera < 0 || p.AccountCera > MaxAmount || p.CharacterGold < 0 || p.CharacterGold > MaxAmount {
		return errors.New("金额必须为 0 至 2147483647 的整数；0 表示关闭")
	}
	return nil
}

// WelcomeCera is called only inside character creation's locked account
// transaction. The amount is captured at registration, never at later login.
func WelcomeCera(account *repository.AccountRecord, amount int64) error {
	if amount < 0 || amount > MaxAmount {
		return errors.New("invalid welcome cera")
	}
	if amount == 0 || account.Metadata[WelcomeReceipt] != "" {
		return nil
	}
	current := int64(0)
	if raw := account.Metadata["account_cera"]; raw != "" {
		var err error
		current, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || current < 0 {
			return errors.New("invalid account cera balance")
		}
	}
	if current > MaxAmount-amount {
		return errors.New("welcome cera exceeds wallet limit")
	}
	if account.Metadata == nil {
		account.Metadata = make(map[string]string)
	}
	account.Metadata["account_cera"] = strconv.FormatInt(current+amount, 10)
	account.Metadata[WelcomeReceipt] = strconv.FormatInt(amount, 10)
	return nil
}

type Recipient struct {
	Delivery             *DeliveryReceipt `json:"delivery,omitempty"`
	SessionGeneration    string           `json:"sessionGeneration,omitempty"`
	AccountID            string           `json:"accountId"`
	CharacterID          string           `json:"characterId"`
	Name                 string           `json:"name"`
	CreatedAt            time.Time        `json:"createdAt"`
	MailID               string           `json:"mailId,omitempty"`
	DeliveredCharacterID string           `json:"deliveredCharacterId,omitempty"`
	Error                string           `json:"error,omitempty"`
}

type Job struct {
	Cera               int64                       `json:"cera,omitempty"`
	ReceiptBasis       *ReceiptBasis               `json:"receiptBasis,omitempty"`
	Definition         *ItemDefinition             `json:"definition,omitempty"`
	RewardPlan         *RewardPlan                 `json:"rewardPlan,omitempty"`
	RecipientSource    *RecipientSource            `json:"recipientSource,omitempty"`
	PVFChecksum        string                      `json:"pvfChecksum,omitempty"`
	RequestFingerprint string                      `json:"requestFingerprint,omitempty"`
	Revision           uint64                      `json:"revision,omitempty"`
	ID                 string                      `json:"id"`
	Gold               int64                       `json:"gold"`
	Title              string                      `json:"title"`
	Body               string                      `json:"body"`
	CreatedAt          time.Time                   `json:"createdAt"`
	Started            bool                        `json:"started"`
	Recipients         []Recipient                 `json:"recipients"`
	CampaignID         string                      `json:"campaignId,omitempty"`
	Scope              GrantScope                  `json:"scope,omitempty"`
	Attachments        []repository.MailAttachment `json:"attachments,omitempty"`
}

func (j Job) Validate() error {
	if err := j.validateRewardPlan(); err != nil {
		return err
	}
	if j.Scope.Effective() != ScopeCharacter && j.Scope.Effective() != ScopeAccount {
		return errors.New("福利范围必须为每账号一次或每角色一次")
	}
	// Conservative UTF-8 byte limits also fit the current GBK mail fields.
	if j.Cera < 0 || j.Cera > MaxAmount || (j.Cera > 0 && (j.Scope != ScopeAccount || j.Gold != 0 || len(j.Attachments) != 0 || j.RewardPlan != nil)) {
		return errors.New("点券福利必须为每账号一次，金额为 1 至 2147483647，单独建规则")
	}
	if j.Gold < 0 || j.Gold > MaxAmount || (j.Cera == 0 && j.Gold == 0 && len(j.Attachments) == 0) {
		return errors.New("福利需要金币、点券或物品；金额范围为 0 至 2147483647")
	}
	if len(j.Attachments) > 1 {
		return errors.New("每条福利规则只支持一种物品，请分别建规则")
	}
	for _, a := range j.Attachments {
		if a.ItemID <= 0 || a.ItemID > math.MaxInt32 || a.Count <= 0 || a.Count > math.MaxInt32 {
			return errors.New("物品 ID 和数量必须为 1 至 2147483647 的整数")
		}
	}
	if j.CampaignID != "" && !validCampaignID(j.CampaignID) {
		return errors.New("无效的福利规则编号")
	}
	if !utf8.ValidString(j.Title) || strings.TrimSpace(j.Title) == "" || len(j.Title) > 60 || strings.ContainsRune(j.Title, 0) || !utf8.ValidString(j.Body) || len(j.Body) > 240 || strings.ContainsRune(j.Body, 0) {
		return errors.New("标题不能为空且最多 60 字节，正文最多 240 字节（中文约 20 / 80 字）")
	}
	return nil
}

// Deliver commits the mail and a permanent receipt together. The receipt
// survives mail deletion/claim, process restart and a lost HTTP response.
func Deliver(ctx context.Context, group repository.Group, job Job, recipient Recipient) (string, error) {
	receipt, err := DeliverWithReceipt(ctx, group, job, recipient)
	return receipt.MailID, err
}

// Account receipts retain the actual delivery target: mailbox IDs alone are
// only unique within one character. Retries for another character return it.
func DeliverWithReceipt(ctx context.Context, group repository.Group, job Job, recipient Recipient) (DeliveryReceipt, error) {
	return DeliverProjectedWithReceipt(ctx, group, job, recipient, nil)
}

func DeliverProjectedWithReceipt(ctx context.Context, group repository.Group, job Job, recipient Recipient, project RewardProjector) (DeliveryReceipt, error) {
	if err := job.Validate(); err != nil {
		return DeliveryReceipt{}, err
	}
	if len(job.ID) != 32 {
		return DeliveryReceipt{}, errors.New("invalid campaign id")
	}
	if _, err := hex.DecodeString(job.ID); err != nil {
		return DeliveryReceipt{}, err
	}
	uow, ok := group.MailboxAssets.(repository.AccountMailboxCharacterUnitOfWork)
	if !ok {
		return DeliveryReceipt{}, repository.ErrMailboxAssetTransactionUnavailable
	}
	key, prefix := receiptIdentity(job, recipient)
	var result DeliveryReceipt
	err := uow.WithinAccountMailboxCharacter(ctx, recipient.AccountID, recipient.CharacterID, func(accounts repository.AccountRepository, character repository.CharacterRecord, mailboxes repository.MailboxRepository) error {
		account, found, err := accounts.Load(ctx, recipient.AccountID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("账号不存在")
		}
		if character.AccountID != recipient.AccountID || character.CharacterID != recipient.CharacterID {
			return errors.New("角色与收件账号不匹配")
		}
		if account.Metadata[key] != "" {
			result, err = PreviousDeliveryReceipt(account, job, recipient)
			return err
		}
		if character.Stats["delete_flag"] != 0 {
			return errors.New("角色已删除，未发送")
		}
		if !character.CreatedAt.Equal(recipient.CreatedAt) {
			return errors.New("角色已重新创建，请重新预览")
		}
		at := time.Now().UTC().Truncate(time.Second)
		if job.Cera > 0 {
			account = repository.CloneAccount(account)
			result, err = creditCampaignCera(&account, job.Cera)
			if err != nil {
				return err
			}
		}
		attachments := job.Attachments
		if job.RewardPlan != nil {
			if project == nil {
				return ErrRewardProjection
			}
			attachments, err = project(job, at)
			if err != nil {
				return err
			}
			if len(attachments) != 1 || attachments[0].ItemID != job.Attachments[0].ItemID || attachments[0].Count != job.Attachments[0].Count {
				return ErrRewardProjection
			}
		}
		deliver := repository.AppendSystemMail
		body := job.Body
		if job.Cera > 0 {
			deliver = repository.AppendSystemNotification
			body = "点券 " + strconv.FormatInt(job.Cera, 10) + " 已存入账号，账号下所有角色共享。"
		}
		mailDone := diagnostics.Start(ctx, "delivery.mail_append")
		mailID, err := deliver(ctx, mailboxes, repository.SystemMailDelivery{
			RecipientCharacterID: recipient.CharacterID, Title: job.Title, Body: body, Source: "test-benefits:" + deliveryID(job), Gold: job.Gold, Attachments: attachments, CreatedAt: at,
		})
		mailDone(err)
		if err != nil {
			return err
		}
		result.MailID, result.CharacterID, result.CreatedAt = mailID, recipient.CharacterID, character.CreatedAt
		if job.Cera > 0 {
			result.GrantedAt = at
		}
		if job.RewardPlan != nil {
			result.GrantedAt, result.Attachments = at, attachments
			result.PVFChecksum = job.PVFChecksum
		}
		payload := mailID
		if job.Scope.Effective() == ScopeAccount || job.RewardPlan != nil {
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			payload = string(raw)
		}
		if job.Cera > 0 {
			for _, field := range []string{"account_cera", "gm_currency_sequence_v1"} {
				walletDone := diagnostics.Start(ctx, "delivery.wallet_write")
				err := repository.SaveAccountMetadataEntry(ctx, accounts, account, field, account.Metadata[field], time.Now().UTC())
				walletDone(err)
				if err != nil {
					return err
				}
			}
		}
		receiptDone := diagnostics.Start(ctx, "delivery.receipt_write")
		err = repository.SaveAccountMetadataEntry(ctx, accounts, account, key, prefix+payload, time.Now().UTC())
		receiptDone(err)
		return err
	})
	if err != nil {
		return DeliveryReceipt{}, err
	}
	return result, nil
}

func validCampaignID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func deliveryID(j Job) string {
	if j.CampaignID != "" {
		return j.CampaignID
	}
	return j.ID
}
func receiptIdentity(j Job, r Recipient) (string, string) {
	if j.ReceiptBasis != nil {
		j.Attachments, j.PVFChecksum = j.ReceiptBasis.Attachments, j.ReceiptBasis.PVFChecksum
	}
	key := "test_benefits_mail_" + deliveryID(j) + "_" + r.CharacterID
	values := []any{j.Gold, j.Title, j.Body, r.AccountID, r.CharacterID, r.CreatedAt.UTC()}
	if j.Scope.Effective() == ScopeAccount {
		key = "test_benefits_account_mail_v1_" + deliveryID(j)
		values = []any{j.Gold, j.Title, j.Body, r.AccountID, ScopeAccount}
	}
	// Preserve old gold-campaign fingerprints exactly. New reusable rules include
	// the character incarnation, so a deleted/recreated ID is a new character.
	if j.CampaignID != "" && j.Scope.Effective() == ScopeCharacter {
		incarnation := sha256.Sum256([]byte(r.CreatedAt.UTC().Format(time.RFC3339Nano)))
		key += "_" + hex.EncodeToString(incarnation[:8])
	}
	if len(j.Attachments) != 0 {
		values = append(values, j.Attachments)
	}
	if j.Cera > 0 {
		key = "test_benefits_account_cera_v1_" + deliveryID(j)
		values = append(values, "cera", j.Cera)
	}
	if j.RewardPlan != nil {
		values = append(values, j.RewardPlan, j.PVFChecksum)
	}
	content, _ := json.Marshal(values)
	fingerprint := sha256.Sum256(content)
	return key, hex.EncodeToString(fingerprint[:]) + ":"
}

func PreviousDelivery(account repository.AccountRecord, job Job, recipient Recipient) (string, error) {
	receipt, err := PreviousDeliveryReceipt(account, job, recipient)
	return receipt.MailID, err
}

func PreviousDeliveryReceipt(account repository.AccountRecord, job Job, recipient Recipient) (DeliveryReceipt, error) {
	if err := job.Validate(); err != nil {
		return DeliveryReceipt{}, err
	}
	key, prefix := receiptIdentity(job, recipient)
	if receipt := account.Metadata[key]; receipt != "" {
		if !strings.HasPrefix(receipt, prefix) {
			return DeliveryReceipt{}, errors.New("福利规则内容与历史记录不一致")
		}
		payload := strings.TrimPrefix(receipt, prefix)
		if job.Scope.Effective() == ScopeAccount || job.RewardPlan != nil {
			var result DeliveryReceipt
			if json.Unmarshal([]byte(payload), &result) != nil || result.MailID == "" || result.CharacterID == "" {
				return DeliveryReceipt{}, errors.New("账号福利回执无效，未重新发放")
			}
			if job.RewardPlan != nil && (result.GrantedAt.IsZero() || len(result.Attachments) != 1 || result.Attachments[0].ItemID != job.Attachments[0].ItemID || result.Attachments[0].Count != job.Attachments[0].Count) {
				return DeliveryReceipt{}, ErrRewardProjection
			}
			if job.Cera > 0 && !validCeraReceipt(result, job.Cera) {
				return DeliveryReceipt{}, errors.New("点券福利回执无效，未重新发放")
			}
			return result, nil
		}
		if payload == "" {
			return DeliveryReceipt{}, errors.New("福利回执缺少邮件编号，未重新发放")
		}
		return DeliveryReceipt{MailID: payload, CharacterID: recipient.CharacterID, CreatedAt: recipient.CreatedAt}, nil
	}
	return DeliveryReceipt{}, nil
}
