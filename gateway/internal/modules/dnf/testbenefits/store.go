package testbenefits

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

// Store uses the registration database; settings, signup entitlements and
// campaign snapshots are included in its ordinary consistent backup.
type Store struct {
	db     *sql.DB
	prefix string
}

// Account-scope records live outside legacy scanner/job tables. An older GM
// must never ignore the scope field and deliver them once to every character.
func (s *Store) scopedTable(kind string, scope GrantScope) string {
	if scope.Effective() == ScopeAccount {
		return s.prefix + "_benefit_acct_" + kind
	}
	return s.prefix + "_benefit_" + kind
}

type payloadReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) readPayload(ctx context.Context, reader payloadReader, kind, id, lock string) ([]byte, string, error) {
	for _, table := range s.payloadTables(kind) {
		var raw []byte
		err := reader.QueryRowContext(ctx, "SELECT payload FROM "+table+" WHERE id=?"+lock, id).Scan(&raw)
		if !errors.Is(err, sql.ErrNoRows) {
			return raw, table, err
		}
	}
	return nil, "", sql.ErrNoRows
}

func (s *Store) payloadListQuery(kind string) string {
	return "SELECT payload FROM (" + s.payloadUnion(kind) + ") records ORDER BY created_at DESC"
}

func (s *Store) payloadUnion(kind string) string {
	parts := []string{}
	for _, table := range s.payloadTables(kind) {
		parts = append(parts, "SELECT id,created_at,payload FROM "+table)
	}
	return strings.Join(parts, " UNION ALL ")
}

func NewStore(db *sql.DB, accountTable string) (*Store, error) {
	if db == nil || len(accountTable) > 40 || !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(accountTable) {
		return nil, errors.New("invalid benefits database/table")
	}
	return &Store{db: db, prefix: accountTable}, nil
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_pvf_ops (sequence BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, request_id CHAR(64) NOT NULL UNIQUE, fingerprint CHAR(64) NOT NULL, campaign_id CHAR(32) NOT NULL, payload LONGTEXT NOT NULL, INDEX campaign_sequence(campaign_id,sequence)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_policy (id INT PRIMARY KEY, revision BIGINT UNSIGNED NOT NULL, cera BIGINT NOT NULL, gold BIGINT NOT NULL) ENGINE=InnoDB",
		"INSERT IGNORE INTO " + s.prefix + "_benefit_policy (id,revision,cera,gold) VALUES (1,0,0,0)",
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_signup (account_id VARCHAR(64) PRIMARY KEY, cera BIGINT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_jobs (id CHAR(32) PRIMARY KEY, created_at DATETIME(6) NOT NULL, payload LONGTEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_campaigns (id CHAR(32) PRIMARY KEY, created_at DATETIME(6) NOT NULL, payload LONGTEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_acct_jobs (id CHAR(32) PRIMARY KEY, created_at DATETIME(6) NOT NULL, payload LONGTEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE IF NOT EXISTS " + s.prefix + "_benefit_acct_campaigns (id CHAR(32) PRIMARY KEY, created_at DATETIME(6) NOT NULL, payload LONGTEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	for _, kind := range []string{"v2_campaigns", "v2_jobs", "v3_campaigns", "v3_jobs"} {
		if _, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.prefix+"_benefit_"+kind+" (id CHAR(32) PRIMARY KEY, created_at DATETIME(6) NOT NULL, payload LONGTEXT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Policy(ctx context.Context) (Policy, error) {
	var p Policy
	err := s.db.QueryRowContext(ctx, "SELECT revision,cera,gold FROM "+s.prefix+"_benefit_policy WHERE id=1").Scan(&p.Revision, &p.AccountCera, &p.CharacterGold)
	if err == nil {
		err = p.Validate()
	}
	return p, err
}

func (s *Store) SavePolicy(ctx context.Context, p Policy) (Policy, error) {
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	r, err := s.db.ExecContext(ctx, "UPDATE "+s.prefix+"_benefit_policy SET revision=revision+1,cera=?,gold=? WHERE id=1 AND revision=?", p.AccountCera, p.CharacterGold, p.Revision)
	if err != nil {
		return Policy{}, err
	}
	count, err := r.RowsAffected()
	if err != nil {
		return Policy{}, err
	}
	if count != 1 {
		return Policy{}, errors.New("设置已被其他窗口修改，请刷新后重试")
	}
	p.Revision++
	return p, nil
}

// CaptureSignup must run in the same SQL transaction as web registration.
// Old accounts have no entitlement. Later policy changes do not alter it.
func (s *Store) CaptureSignup(ctx context.Context, tx *sql.Tx, accountID string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO "+s.prefix+"_benefit_signup (account_id,cera) SELECT ?,cera FROM "+s.prefix+"_benefit_policy WHERE id=1", accountID)
	return err
}

func (s *Store) SignupCera(ctx context.Context, accountID string) (int64, error) {
	var amount int64
	err := s.db.QueryRowContext(ctx, "SELECT cera FROM "+s.prefix+"_benefit_signup WHERE account_id=?", accountID).Scan(&amount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return amount, err
}

// Preview freezes the existing registered accounts and their existing roles.
// It never sends mail. A bounded complete scan fails rather than truncating.
func (s *Store) Preview(ctx context.Context, group repository.Group, job Job) (Job, error) {
	job, err := s.freezeRecipients(ctx, group, job)
	if err != nil {
		return Job{}, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return Job{}, err
	}
	job.ID = hex.EncodeToString(id[:])
	job.CreatedAt = time.Now().UTC()
	job.Started = false
	data, err := json.Marshal(job)
	if err != nil {
		return Job{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO "+s.recordTable("jobs", job.Scope, data)+" (id,created_at,payload) VALUES (?,?,?)", job.ID, job.CreatedAt, string(data))
	return job, err
}

func (s *Store) freezeRecipients(ctx context.Context, group repository.Group, job Job) (Job, error) {
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT account_id FROM "+s.prefix+" ORDER BY id LIMIT 1001")
	if err != nil {
		return Job{}, err
	}
	var accounts []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Job{}, err
		}
		accounts = append(accounts, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Job{}, err
	}
	if len(accounts) > 1000 {
		return Job{}, ErrPreviewLimit
	}
	job.Recipients = make([]Recipient, 0)
	for _, accountID := range accounts {
		var account repository.AccountRecord
		if job.CampaignID != "" {
			var found bool
			account, found, err = group.Account.Load(ctx, accountID)
			if err != nil {
				return Job{}, err
			}
			if !found {
				continue
			}
		}
		characters, err := CharactersForAccount(ctx, group, accountID)
		if err != nil {
			return Job{}, err
		}
		if len(characters) > repository.DefaultCharacterSlotLimit {
			return Job{}, errors.New("账号角色数量超出当前支持范围，未生成不完整名单")
		}
		// Account rules freeze one explicit target in preview. Automatic delivery
		// chooses the first eligible role encountered; the account lock resolves
		// concurrent candidates, and its durable receipt records the actual role.
		sort.SliceStable(characters, func(i, k int) bool {
			if characters[i].Slot != characters[k].Slot {
				return characters[i].Slot < characters[k].Slot
			}
			return characters[i].CharacterID < characters[k].CharacterID
		})
		for _, c := range characters {
			if c.AccountID != accountID {
				return Job{}, errors.New("角色归属与账号不一致")
			}
			if c.Stats["delete_flag"] != 0 {
				continue
			}
			if len(job.Recipients) >= 5000 {
				return Job{}, ErrPreviewLimit
			}
			r := Recipient{AccountID: accountID, CharacterID: c.CharacterID, Name: c.Name, CreatedAt: c.CreatedAt}
			if job.CampaignID != "" {
				var receipt DeliveryReceipt
				receipt, err = PreviousDeliveryReceipt(account, job, r)
				if err != nil {
					return Job{}, err
				}
				r.MailID, r.DeliveredCharacterID = receipt.MailID, receipt.CharacterID
				if (job.RewardPlan != nil || job.Cera > 0) && receipt.MailID != "" {
					r.Delivery = &receipt
				}
			}
			job.Recipients = append(job.Recipients, r)
			if job.Scope.Effective() == ScopeAccount {
				break
			}
		}
	}
	sort.Slice(job.Recipients, func(i, j int) bool { return job.Recipients[i].CharacterID < job.Recipients[j].CharacterID })
	if len(job.Recipients) == 0 {
		return Job{}, ErrPreviewEmpty
	}
	return job, nil
}

func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, s.payloadListQuery("jobs")+" LIMIT 20")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		var raw []byte
		var j Job
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &j); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// Run processes at most ten recipients while holding the job row lock. Domain
// receipts cover the crash window between asset commit and progress commit.
// Failed recipients are retried only by an explicit retry request.
func (s *Store) Run(ctx context.Context, group repository.Group, id string, retry bool) (Job, error) {
	return s.run(ctx, group, id, retry, nil)
}

// RunVersion advances exactly the reviewed revision; stale browser requests
// cannot start another batch or retry a newer set of failures.
func (s *Store) RunVersion(ctx context.Context, group repository.Group, id string, retry bool, revision uint64) (Job, error) {
	return s.run(ctx, group, id, retry, &revision)
}

func (s *Store) run(ctx context.Context, group repository.Group, id string, retry bool, revision *uint64) (Job, error) {
	return s.runProjected(ctx, group, id, retry, revision, nil)
}

func (s *Store) RunVersionProjected(ctx context.Context, group repository.Group, id string, retry bool, revision uint64, project RewardProjector) (Job, error) {
	return s.runProjected(ctx, group, id, retry, &revision, project)
}

func (s *Store) runProjected(ctx context.Context, group repository.Group, id string, retry bool, revision *uint64, project RewardProjector) (Job, error) {
	if len(id) != 32 {
		return Job{}, errors.New("无效的发放编号")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	data, table, err := s.readPayload(ctx, tx, "jobs", id, " FOR UPDATE")
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err = json.Unmarshal(data, &j); err != nil {
		return Job{}, err
	}
	if err = j.Validate(); err != nil {
		return Job{}, err
	}
	if revision != nil && j.Revision != *revision {
		return Job{}, ErrRevision
	}
	if j.CampaignID != "" {
		campaign, err := s.lockCampaign(ctx, tx, j.CampaignID)
		if err != nil {
			return Job{}, err
		}
		if !campaign.Enabled {
			return Job{}, ErrCampaignDisabled
		}
		j, err = AdoptCampaignReward(j, campaign)
		if err != nil {
			return Job{}, err
		}
	}
	if retry {
		for i := range j.Recipients {
			if j.Recipients[i].MailID == "" {
				j.Recipients[i].Error = ""
			}
		}
	}
	j.Started = true
	j.Revision++
	count := 0
	for i := range j.Recipients {
		r := &j.Recipients[i]
		if r.MailID != "" || r.Error != "" {
			continue
		}
		receipt, sendErr := DeliverProjectedWithReceipt(ctx, group, j, *r, project)
		if sendErr != nil {
			r.Error = fmt.Sprint(sendErr)
		} else {
			r.MailID = receipt.MailID
			r.DeliveredCharacterID = receipt.CharacterID
			if j.RewardPlan != nil || j.Cera > 0 {
				r.Delivery = &receipt
			}
			r.Error = ""
		}
		count++
		if count == 10 {
			break
		}
	}
	data, err = json.Marshal(j)
	if err != nil {
		return Job{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE "+table+" SET payload=? WHERE id=?", string(data), id); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}
