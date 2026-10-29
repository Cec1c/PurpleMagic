package testbenefits

import (
	"context"
	"database/sql"
	"encoding/json"
)

// DeleteCampaign waits for in-flight delivery locks, stops future delivery and
// hides the rule from lists. Its identity, jobs and permanent receipts remain.
func (s *Store) DeleteCampaign(ctx context.Context, id string, revision uint64) (Campaign, error) {
	if !validCampaignID(id) {
		return Campaign{}, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Campaign{}, err
	}
	defer tx.Rollback()
	raw, table, err := s.readPayload(ctx, tx, "campaigns", id, " FOR UPDATE")
	if err != nil {
		return Campaign{}, err
	}
	var c Campaign
	if err = json.Unmarshal(raw, &c); err != nil {
		return Campaign{}, err
	}
	if c.Deleted {
		return c, tx.Commit()
	}
	if c.Revision != revision {
		return Campaign{}, ErrRevision
	}
	c.Deleted = true
	c.Enabled = false
	c.Revision++
	raw, err = json.Marshal(c)
	if err != nil {
		return Campaign{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE "+table+" SET payload=? WHERE id=?", raw, id); err != nil {
		return Campaign{}, err
	}
	return c, tx.Commit()
}
