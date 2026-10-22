package testbenefits

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// ReceiptBasis freezes the original fingerprint inputs across approved PVF
// revisions. Reward intent (scope, item/count, text, gold, options) is immutable.
type ReceiptBasis struct {
	PVFChecksum string                      `json:"pvfChecksum"`
	Attachments []repository.MailAttachment `json:"attachments"`
}

// ScriptSHA256 hashes the decoded script, not the full PVF. Missing historical
// evidence stays absent; it is never inferred from the current asset.
type ItemDefinition struct {
	UsablePeriodDays   int64  `json:"usablePeriodDays"`
	AbsoluteExpireUnix int64  `json:"absoluteExpireUnix"`
	ScriptSHA256       string `json:"scriptSha256"`
	Path               string `json:"path"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	Tag                string `json:"tag"`
	Rarity             int    `json:"rarity"`
	MinLevel           int    `json:"minLevel"`
	Grade              int    `json:"grade"`
	StackLimit         int64  `json:"stackLimit"`
}

type PVFProjection struct {
	PVFChecksum string                      `json:"pvfChecksum"`
	Attachments []repository.MailAttachment `json:"attachments"`
	Definition  *ItemDefinition             `json:"definition"`
	ItemName    string                      `json:"itemName"`
}

func CampaignProjection(c Campaign) PVFProjection {
	return PVFProjection{c.Reward.PVFChecksum, c.Reward.Attachments, c.Reward.Definition, c.ItemName}
}

// A full-archive update can leave a rule's item unchanged. Rebind only when
// both its recorded definition and its projected attachment match exactly.
// Missing historical evidence or changed reward data still requires review.
func UnchangedCampaignProjection(c Campaign, p PVFProjection) bool {
	return c.Reward.RewardPlan != nil && c.Reward.Definition != nil &&
		len(c.Reward.Definition.ScriptSHA256) == 64 && p.Definition != nil &&
		reflect.DeepEqual(c.Reward.Definition, p.Definition) &&
		reflect.DeepEqual(c.Reward.Attachments, p.Attachments) && c.ItemName == p.ItemName
}

func PVFProjectionHash(p PVFProjection) string { return pvfHash(p) }
func pvfHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type PVFReviewCommand struct {
	CampaignID     string `json:"campaignId"`
	Revision       uint64 `json:"revision"`
	TargetChecksum string `json:"targetChecksum"`
	ProjectionHash string `json:"projectionHash"`
	PlanToken      string `json:"planToken"`
}

func PVFReviewIdentity(c PVFReviewCommand, key string) string {
	c.PlanToken = ""
	return pvfHash(struct {
		Command PVFReviewCommand
		Key     string
	}{c, key})
}

type PVFReview struct {
	Sequence         uint64        `json:"sequence"`
	CampaignID       string        `json:"campaignId"`
	CreatedAt        time.Time     `json:"createdAt"`
	Actor            string        `json:"actor"`
	PreviousRevision uint64        `json:"previousRevision"`
	Revision         uint64        `json:"revision"`
	Before           PVFProjection `json:"before"`
	After            PVFProjection `json:"after"`
}

var ErrPVFReview = errors.New("福利物品版本或核对内容已变化，请重新核对")

func (s *Store) CheckPVFReviewSchema(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,request_id,fingerprint,campaign_id,payload FROM "+s.prefix+"_benefit_pvf_ops LIMIT 0")
	if err != nil {
		return err
	}
	return rows.Close()
}

// ApplyCampaignPVF serializes with both manual batches and automatic delivery.
// Replay is checked before resolving any live PVF or expiring preview token.
func (s *Store) ApplyCampaignPVF(ctx context.Context, key string, in PVFReviewCommand, actor string, resolve func(Campaign) (PVFProjection, error)) (PVFReview, error) {
	if !validCampaignID(in.CampaignID) || len(key) < 16 || len(key) > 128 {
		return PVFReview{}, ErrIdempotency
	}
	id := pvfHash("campaign-pvf:" + key)
	fp := pvfHash(in)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return PVFReview{}, err
	}
	defer tx.Rollback()
	var singleton uint64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM "+s.prefix+"_benefit_policy WHERE id=1 FOR UPDATE").Scan(&singleton); err != nil {
		return PVFReview{}, err
	}
	var raw []byte
	var priorFP string
	var seq uint64
	table := s.prefix + "_benefit_pvf_ops"
	err = tx.QueryRowContext(ctx, "SELECT sequence,fingerprint,payload FROM "+table+" WHERE request_id=?", id).Scan(&seq, &priorFP, &raw)
	if err == nil {
		if fp != priorFP {
			return PVFReview{}, ErrIdempotency
		}
		var result PVFReview
		if err = json.Unmarshal(raw, &result); err != nil {
			return PVFReview{}, err
		}
		result.Sequence = seq
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PVFReview{}, err
	}
	raw, campaignTable, err := s.readPayload(ctx, tx, "campaigns", in.CampaignID, " FOR UPDATE")
	if err != nil {
		return PVFReview{}, err
	}
	var c Campaign
	if err = json.Unmarshal(raw, &c); err != nil {
		return PVFReview{}, err
	}
	if c.Revision != in.Revision {
		return PVFReview{}, ErrRevision
	}
	if c.Reward.RewardPlan == nil || resolve == nil {
		return PVFReview{}, ErrPVFReview
	}
	if err = c.Reward.Validate(); err != nil {
		return PVFReview{}, err
	}
	projection, err := resolve(c)
	if err != nil {
		return PVFReview{}, err
	}
	if projection.PVFChecksum != in.TargetChecksum || projection.PVFChecksum == c.Reward.PVFChecksum || PVFProjectionHash(projection) != in.ProjectionHash || projection.Definition == nil || len(projection.Definition.ScriptSHA256) != 64 {
		return PVFReview{}, ErrPVFReview
	}
	result := PVFReview{CampaignID: c.ID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Actor: actor, PreviousRevision: c.Revision, Revision: c.Revision + 1, Before: CampaignProjection(c), After: projection}
	if c.Reward.ReceiptBasis == nil {
		c.Reward.ReceiptBasis = &ReceiptBasis{PVFChecksum: c.Reward.PVFChecksum, Attachments: c.Reward.Attachments}
	}
	c.Reward.PVFChecksum, c.Reward.Attachments, c.Reward.Definition = projection.PVFChecksum, projection.Attachments, projection.Definition
	c.ItemName, c.Revision = projection.ItemName, result.Revision
	if err = c.Reward.Validate(); err != nil {
		return PVFReview{}, err
	}
	raw, err = json.Marshal(c)
	if err != nil {
		return PVFReview{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE "+campaignTable+" SET payload=? WHERE id=?", raw, c.ID); err != nil {
		return PVFReview{}, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return PVFReview{}, err
	}
	saved, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(request_id,fingerprint,campaign_id,payload) VALUES(?,?,?,?)", id, fp, c.ID, raw)
	if err != nil {
		return PVFReview{}, err
	}
	sequence, err := saved.LastInsertId()
	if err != nil {
		return PVFReview{}, err
	}
	result.Sequence = uint64(sequence)
	return result, tx.Commit()
}

func (s *Store) CampaignPVFHistory(ctx context.Context, id string, before uint64, limit int) ([]PVFReview, error) {
	if !validCampaignID(id) || limit < 1 || limit > 101 {
		return nil, ErrPVFReview
	}
	query := "SELECT sequence,payload FROM " + s.prefix + "_benefit_pvf_ops WHERE campaign_id=?"
	args := []any{id}
	if before > 0 {
		query += " AND sequence<?"
		args = append(args, before)
	}
	query += " ORDER BY sequence DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PVFReview{}
	for rows.Next() {
		var r PVFReview
		var seq uint64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		r.Sequence = seq
		result = append(result, r)
	}
	return result, rows.Err()
}

// The job retains its list, progress and revision. Only a rule with the same
// immutable intent and original receipt basis may replace its projection.
func AdoptCampaignReward(j Job, c Campaign) (Job, error) {
	if j.RewardPlan == nil {
		return j, nil
	}
	r := c.Reward
	if j.CampaignID != c.ID || r.CampaignID != c.ID || j.Scope.Effective() != r.Scope.Effective() || j.Gold != r.Gold || j.Title != r.Title || j.Body != r.Body || !reflect.DeepEqual(j.RewardPlan, r.RewardPlan) {
		return Job{}, ErrPVFReview
	}
	basis := func(v Job) ReceiptBasis {
		if v.ReceiptBasis != nil {
			return *v.ReceiptBasis
		}
		return ReceiptBasis{PVFChecksum: v.PVFChecksum, Attachments: v.Attachments}
	}
	if !reflect.DeepEqual(basis(j), basis(r)) {
		return Job{}, ErrPVFReview
	}
	j.Attachments, j.PVFChecksum, j.ReceiptBasis, j.Definition = r.Attachments, r.PVFChecksum, r.ReceiptBasis, r.Definition
	return j, j.Validate()
}
