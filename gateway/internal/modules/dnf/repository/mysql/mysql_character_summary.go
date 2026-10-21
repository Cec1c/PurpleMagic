package mysql

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"unicode/utf8"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var _ repository.CharacterSummaryReader = (*mysqlCharStore)(nil)

const summaryColumns = "character_id,account_id,name,job,level,grow_type,slot,created_at,updated_at"

func scanCharacterSummary(row interface{ Scan(...any) error }) (repository.CharacterSummary, error) {
	var c repository.CharacterSummary
	var created, updated sql.NullTime
	err := row.Scan(&c.CharacterID, &c.AccountID, &c.Name, &c.Job, &c.Level, &c.GrowType, &c.Slot, &created, &updated)
	if created.Valid {
		c.CreatedAt = created.Time.UTC()
	}
	if updated.Valid {
		c.UpdatedAt = updated.Time.UTC()
	}
	return c, err
}

// One bounded base-table query per configured shard, independent of the number
// of accounts or roles on the current page. Never call Load/ListByAccount here.
func (s *mysqlCharStore) SummariesForAccounts(ctx context.Context, ids []string) ([]repository.CharacterSummary, error) {
	out := []repository.CharacterSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		return nil, repository.ErrCharacterSummaryRange
	}
	allowed := make(map[string]bool, len(ids))
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		if id == "" || id != strings.TrimSpace(id) || len(id) > 128 || !utf8.ValidString(id) {
			return nil, repository.ErrCharacterSummaryRange
		}
		if !allowed[id] {
			allowed[id] = true
			args = append(args, id)
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	bound := len(args) * repository.DefaultCharacterSlotLimit
	args = append(args, args...)
	args = append(args, bound+1)
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, database := range s.router.readDBs {
		table := mysqlTable(database, s.router.tablePrefix, mysqlCharTable)
		rows, err := diagnosticQuery(ctx, s.router.db, "characters.summary", "SELECT "+summaryColumns+" FROM "+table+" WHERE delete_flag=0 AND account_id IN ("+marks+") AND BINARY account_id IN ("+marks+") ORDER BY BINARY account_id,slot,BINARY character_id LIMIT ?", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			c, e := scanCharacterSummary(rows)
			if e != nil {
				rows.Close()
				return nil, e
			}
			expected, e := s.router.readTable(mysqlCharTable, c.CharacterID)
			counts[c.AccountID]++
			if e != nil || expected != table || !allowed[c.AccountID] || seen[c.CharacterID] || counts[c.AccountID] > repository.DefaultCharacterSlotLimit || len(out) >= bound {
				rows.Close()
				return nil, repository.ErrCharacterSummaryRange
			}
			seen[c.CharacterID] = true
			out = append(out, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		return out[i].CharacterID < out[j].CharacterID
	})
	return out, nil
}

func (s *mysqlCharStore) SearchSummaries(ctx context.Context, query, after string, limit int) ([]repository.CharacterSummary, error) {
	return s.searchSummaries(ctx, query, after, limit, nil, "")
}

// Presence IDs come from the complete authoritative runtime snapshot. Apply the
// filter before LIMIT, including across shards, rather than filtering one page.
func (s *mysqlCharStore) SearchSummariesByPresence(ctx context.Context, query, after string, limit int, ids []string, online bool) ([]repository.CharacterSummary, error) {
	if len(ids) > 65535 {
		return nil, repository.ErrCharacterSummaryRange
	}
	if len(ids) == 0 && online {
		return []repository.CharacterSummary{}, nil
	}
	clause := ""
	if len(ids) > 0 {
		operator := " IN "
		if !online {
			operator = " NOT IN "
		}
		clause = " AND BINARY character_id" + operator + "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	}
	return s.searchSummaries(ctx, query, after, limit, ids, clause)
}

func (s *mysqlCharStore) searchSummaries(ctx context.Context, query, after string, limit int, ids []string, presenceClause string) ([]repository.CharacterSummary, error) {
	if limit < 1 || limit > 101 || len(query) > 128 || len(after) > 128 || !utf8.ValidString(query) || !utf8.ValidString(after) {
		return nil, repository.ErrCharacterSummaryRange
	}
	pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(query) + "%"
	out := []repository.CharacterSummary{}
	seen := map[string]bool{}
	for _, database := range s.router.readDBs {
		table := mysqlTable(database, s.router.tablePrefix, mysqlCharTable)
		args := []any{after, pattern, query}
		for _, id := range ids {
			args = append(args, id)
		}
		args = append(args, limit)
		rows, err := s.router.db.QueryContext(ctx, "SELECT "+summaryColumns+" FROM "+table+" WHERE delete_flag=0 AND BINARY character_id > ? AND (name LIKE ? ESCAPE '!' OR BINARY character_id=?)"+presenceClause+" ORDER BY BINARY character_id LIMIT ?", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			c, e := scanCharacterSummary(rows)
			if e != nil {
				rows.Close()
				return nil, e
			}
			expected, e := s.router.readTable(mysqlCharTable, c.CharacterID)
			if e != nil || expected != table || seen[c.CharacterID] {
				rows.Close()
				return nil, repository.ErrCharacterSummaryRange
			}
			seen[c.CharacterID] = true
			out = append(out, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CharacterID < out[j].CharacterID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *mysqlCharStore) FindSummary(ctx context.Context, id string) (repository.CharacterSummary, bool, error) {
	if id == "" || id != strings.TrimSpace(id) || len(id) > 128 || !utf8.ValidString(id) {
		return repository.CharacterSummary{}, false, repository.ErrCharacterSummaryRange
	}
	table, err := s.router.readTable(mysqlCharTable, id)
	if err != nil {
		return repository.CharacterSummary{}, false, err
	}
	c, err := scanCharacterSummary(s.router.db.QueryRowContext(ctx, "SELECT "+summaryColumns+" FROM "+table+" WHERE character_id=? AND BINARY character_id=? AND delete_flag=0", id, id))
	if err == sql.ErrNoRows {
		return repository.CharacterSummary{}, false, nil
	}
	return c, err == nil, err
}
