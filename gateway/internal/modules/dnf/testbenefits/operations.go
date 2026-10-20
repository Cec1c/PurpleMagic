package testbenefits

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var (
	ErrIdempotency      = errors.New("请求编号已用于其他内容，请重新预览")
	ErrRevision         = errors.New("记录已更新，请刷新后继续")
	ErrCampaignDisabled = errors.New("福利规则已停用，请先启用再发放")
	ErrRecipient        = errors.New("角色不存在、已删除或归属已改变，请重新选择")
	ErrPreviewEmpty     = errors.New("没有可接收邮件的角色")
	ErrPreviewLimit     = errors.New("预览超出 1000 个账号或 5000 个角色，请缩小发放范围")
)

// A permanent content-bound key shares the record's transaction. There is no
// crash window between creating a rule and persisting its idempotency receipt.
// Lock the existing policy singleton to serialize cross-scope key creation.
func (s *Store) createOnce(ctx context.Context, kind, key string, input any, build func(string, string) (GrantScope, any, error)) ([]byte, error) {
	if len(key) < 16 || len(key) > 128 {
		return nil, ErrIdempotency
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(sum[:])
	sum = sha256.Sum256([]byte("gm-v1:" + kind + ":" + key))
	id := hex.EncodeToString(sum[:16])
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision uint64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM "+s.prefix+"_benefit_policy WHERE id=1 FOR UPDATE").Scan(&revision); err != nil {
		return nil, err
	}
	raw, _, err := s.readPayload(ctx, tx, kind, id, "")
	if err == nil {
		var stored struct {
			RequestFingerprint string `json:"requestFingerprint"`
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		if stored.RequestFingerprint != fingerprint {
			return nil, ErrIdempotency
		}
		return raw, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	scope, value, err := build(id, fingerprint)
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var record struct {
		CreatedAt time.Time `json:"createdAt"`
	}
	if err = json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO "+s.recordTable(kind, scope, raw)+" (id,created_at,payload) VALUES (?,?,?)", id, record.CreatedAt, raw); err != nil {
		return nil, err
	}
	return raw, tx.Commit()
}

func (s *Store) CreateCampaignOnce(ctx context.Context, key string, reward Job, name string, automatic bool) (Campaign, error) {
	if reward.RewardPlan != nil {
		return Campaign{}, ErrRewardProjection
	}
	if err := reward.Validate(); err != nil {
		return Campaign{}, err
	}
	reward = Job{Scope: reward.Scope.Effective(), Gold: reward.Gold, Cera: reward.Cera, Title: reward.Title, Body: reward.Body, Attachments: reward.Attachments}
	input := struct {
		Reward    Job
		Automatic bool
	}{reward, automatic}
	raw, err := s.createOnce(ctx, "campaigns", key, input, func(id, fp string) (GrantScope, any, error) {
		now := time.Now().UTC().Truncate(time.Microsecond)
		reward.ID = id
		reward.CampaignID = id
		reward.CreatedAt = now
		return reward.Scope, Campaign{ID: id, RequestFingerprint: fp, CreatedAt: now, AutoNewCharacters: automatic, ItemName: name, Reward: reward}, nil
	})
	var c Campaign
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	return c, err
}

func (s *Store) PreviewCampaignOnce(ctx context.Context, group repository.Group, id, key string) (Job, error) {
	raw, err := s.createOnce(ctx, "jobs", key, struct{ CampaignID string }{id}, func(jobID, fp string) (GrantScope, any, error) {
		c, err := s.Campaign(ctx, id)
		if err != nil {
			return "", nil, err
		}
		j, err := s.freezeRecipients(ctx, group, c.Reward)
		if err != nil {
			return "", nil, err
		}
		j.ID = jobID
		j.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
		j.RequestFingerprint = fp
		return j.Scope, j, nil
	})
	var j Job
	if err == nil {
		err = json.Unmarshal(raw, &j)
	}
	return j, err
}

// Freeze one current character. DeliverWithReceipt rechecks ownership, deletion
// and incarnation inside the authoritative mailbox/account transaction.
func (s *Store) PreviewMailOnce(ctx context.Context, group repository.Group, key, accountID, characterID string, reward Job) (Job, error) {
	if err := reward.Validate(); err != nil {
		return Job{}, err
	}
	reward = Job{Scope: ScopeCharacter, Gold: reward.Gold, Cera: reward.Cera, Title: reward.Title, Body: reward.Body, Attachments: reward.Attachments}
	input := struct {
		AccountID, CharacterID string
		Reward                 Job
	}{accountID, characterID, reward}
	raw, err := s.createOnce(ctx, "jobs", key, input, func(id, fp string) (GrantScope, any, error) {
		c, found, err := group.Character.Load(ctx, characterID)
		if err != nil {
			return "", nil, err
		}
		if !found || c.AccountID != accountID || c.Stats["delete_flag"] != 0 {
			return "", nil, ErrRecipient
		}
		reward.ID = id
		reward.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
		reward.RequestFingerprint = fp
		reward.Recipients = []Recipient{{AccountID: c.AccountID, CharacterID: c.CharacterID, Name: c.Name, CreatedAt: c.CreatedAt}}
		return reward.Scope, reward, nil
	})
	var j Job
	if err == nil {
		err = json.Unmarshal(raw, &j)
	}
	return j, err
}

func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	if !validCampaignID(id) {
		return Job{}, sql.ErrNoRows
	}
	raw, _, err := s.readPayload(ctx, s.db, "jobs", id, "")
	var j Job
	if err == nil {
		err = json.Unmarshal(raw, &j)
	}
	return j, err
}

// Lists decode one bounded job at a time and retain summaries, never an entire
// page of frozen recipient arrays. Cursor order matches DATETIME(6) precision.
type JobSummary struct {
	RewardPlan      *RewardPlan                 `json:"rewardPlan,omitempty"`
	RecipientSource *RecipientSource            `json:"recipientSource,omitempty"`
	PVFChecksum     string                      `json:"pvfChecksum,omitempty"`
	ID              string                      `json:"id"`
	Revision        uint64                      `json:"revision"`
	CampaignID      string                      `json:"campaignId,omitempty"`
	Scope           GrantScope                  `json:"scope"`
	Title           string                      `json:"title"`
	Body            string                      `json:"body"`
	Gold            int64                       `json:"gold"`
	Cera            int64                       `json:"cera,omitempty"`
	Attachments     []repository.MailAttachment `json:"attachments"`
	CreatedAt       time.Time                   `json:"createdAt"`
	State           string                      `json:"state"`
	Total           int                         `json:"total"`
	Sent            int                         `json:"sent"`
	Failed          int                         `json:"failed"`
	Pending         int                         `json:"pending"`
}

func Summarize(j Job) JobSummary {
	s := JobSummary{ID: j.ID, Revision: j.Revision, CampaignID: j.CampaignID, Scope: j.Scope.Effective(), Title: j.Title, Body: j.Body, Gold: j.Gold, Cera: j.Cera, Attachments: j.Attachments, CreatedAt: j.CreatedAt, Total: len(j.Recipients), State: "preview"}
	for _, r := range j.Recipients {
		if r.MailID != "" {
			s.Sent++
		} else if r.Error != "" {
			s.Failed++
		} else {
			s.Pending++
		}
	}
	s.PVFChecksum = j.PVFChecksum
	s.RecipientSource = j.RecipientSource
	s.RewardPlan = j.RewardPlan
	if j.Started {
		s.State = "running"
		if s.Pending == 0 {
			if s.Failed > 0 {
				s.State = "partial"
			} else {
				s.State = "completed"
			}
		}
	}
	if s.Pending == 0 && s.Failed == 0 {
		s.State = "completed"
	}
	return s
}
func (s *Store) pagePayloads(ctx context.Context, kind, after string, limit int, consume func([]byte) error) (string, error) {
	if limit < 1 || limit > 100 {
		return "", errors.New("invalid page size")
	}
	var ts time.Time
	var id string
	if after != "" {
		parts := strings.Split(after, "|")
		if len(parts) != 2 || !validCampaignID(parts[1]) {
			return "", errors.New("invalid cursor")
		}
		var err error
		ts, err = time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return "", err
		}
		id = parts[1]
	}
	query := "SELECT id,created_at,payload FROM (" + s.payloadUnion(kind) + ") records"
	args := []any{}
	if after != "" {
		query += " WHERE created_at<? OR (created_at=? AND id<?)"
		args = append(args, ts, ts, id)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	last := ""
	n := 0
	for rows.Next() {
		var raw []byte
		var key string
		var created time.Time
		if err = rows.Scan(&key, &created, &raw); err != nil {
			return "", err
		}
		if n == limit {
			return last, nil
		}
		if err = consume(raw); err != nil {
			return "", err
		}
		last = created.UTC().Format(time.RFC3339Nano) + "|" + key
		n++
	}
	return "", rows.Err()
}
func (s *Store) JobPage(ctx context.Context, after string, limit int) ([]JobSummary, string, error) {
	result := []JobSummary{}
	next, err := s.pagePayloads(ctx, "jobs", after, limit, func(raw []byte) error {
		var j Job
		if err := json.Unmarshal(raw, &j); err != nil {
			return err
		}
		result = append(result, Summarize(j))
		return nil
	})
	return result, next, err
}
func (s *Store) CampaignPage(ctx context.Context, after string, limit int) ([]Campaign, string, error) {
	result := []Campaign{}
	next, err := s.pagePayloads(ctx, "campaigns", after, limit, func(raw []byte) error {
		var c Campaign
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		c.RequestFingerprint = ""
		result = append(result, c)
		return nil
	})
	return result, next, err
}
