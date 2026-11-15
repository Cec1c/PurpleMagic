package mysql

import (
	"context"
	"database/sql"
	"fmt"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

var _ repository.CharacterResourceReader = (*mysqlCharStore)(nil)
var _ repository.CharacterBatchInventoryReader = (*mysqlCharStore)(nil)

func (s *mysqlCharStore) resourceSnapshot(ctx context.Context) (*mysqlCharStore, func(), error) {
	beginner, ok := s.router.db.(sqlTxBeginner)
	if !ok {
		return nil, nil, repository.ErrResourceRange
	}
	tx, err := beginner.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	router := s.router
	router.db = sqlTransactionDB{tx: tx}
	return &mysqlCharStore{mysqlStoreBase: mysqlStoreBase{router: router}}, func() { _ = tx.Rollback() }, nil
}

func (s *mysqlCharStore) InventoryPage(ctx context.Context, c repository.CharacterSummary, container string, after, limit int, detail bool) ([]repository.InventorySlot, error) {
	if detail {
		snapshot, close, err := s.resourceSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		defer close()
		current, found, err := snapshot.FindSummary(ctx, c.CharacterID)
		if err != nil {
			return nil, err
		}
		if !found || current.AccountID != c.AccountID {
			return nil, repository.ErrResourceRange
		}
		return snapshot.inventoryPage(ctx, current, container, after, limit, detail)
	}
	return s.inventoryPage(ctx, c, container, after, limit, detail)
}

func (s *mysqlCharStore) inventoryPage(ctx context.Context, c repository.CharacterSummary, container string, after, limit int, detail bool) ([]repository.InventorySlot, error) {
	spec, ok := repository.ResourceContainer(container)
	if !ok || limit < 1 || limit > 101 || after < -1 || after > 32767 || (detail && limit != 1) {
		return nil, repository.ErrResourceRange
	}
	owner, column := c.CharacterID, "character_id"
	itemsName, extraName := mysqlInventoryItemsTable, mysqlInventoryExtraTable
	slotExpr := "CAST(SUBSTRING_INDEX(entry_key,':',-1) AS UNSIGNED)"
	countExpr := "item_count"
	if spec.Owner == "account" {
		owner, column = c.AccountID, "account_id"
		itemsName, extraName = mysqlAccountInventoryItemsTable, mysqlAccountInventoryExtraTable
	}
	if spec.Owner == "equipment" {
		itemsName, extraName = mysqlEquipmentEntriesTable, mysqlEquipmentExtraTable
		slotExpr, countExpr = "slot_index", "1"
	}
	if owner == "" {
		return nil, repository.ErrResourceRange
	}
	items, err := s.router.readTable(itemsName, owner)
	if err != nil {
		return nil, err
	}
	extra, err := s.router.readTable(extraName, owner)
	if err != nil {
		return nil, err
	}
	where := column + "=? AND BINARY " + column + "=?"
	args := []any{owner, owner}
	if spec.Collection != "" {
		where += " AND collection_name=?"
		args = append(args, spec.Collection)
	}
	baseWhere, baseArgs := where, append([]any(nil), args...)
	if spec.Owner != "equipment" {
		where += " AND entry_key REGEXP ?"
		args = append(args, fmt.Sprintf("^%d:(0|[1-9][0-9]*)$", spec.List))
	}
	where += " AND " + slotExpr + " BETWEEN ? AND ?"
	if container != "shared" {
		where += " AND item_id>0"
	}
	args = append(args, spec.First, spec.Last)
	if detail {
		where += " AND " + slotExpr + "=?"
		args = append(args, after)
	} else {
		where += " AND " + slotExpr + ">?"
		args = append(args, after)
	}
	rawExpr := "NULL,0"
	if detail {
		rawExpr = "IF(OCTET_LENGTH(raw_entry)<=65536,raw_entry,NULL),COALESCE(OCTET_LENGTH(raw_entry),0)"
	}
	query := "SELECT entry_key," + slotExpr + ",item_id," + countExpr + ",bind_flag,expire_at," + rawExpr + " FROM " + items + " WHERE " + where + " ORDER BY " + slotExpr + ",BINARY entry_key LIMIT ?"
	args = append(args, limit)
	rows, err := s.router.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []repository.InventorySlot{}
	for rows.Next() {
		var row repository.InventorySlot
		var expiry sql.NullTime
		var rawSize int
		if err = rows.Scan(&row.Key, &row.Slot, &row.Stack.ItemID, &row.Stack.Count, &row.Stack.Bind, &expiry, &row.Stack.RawEntry, &rawSize); err != nil {
			rows.Close()
			return nil, err
		}
		if rawSize > 65536 {
			row.Stack.RawEntry = nil
		}
		if len(out) > 0 && out[len(out)-1].Slot == row.Slot {
			continue
		}
		row.Stack.ExpireAt = scanTime(expiry)
		out = append(out, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !detail || len(out) == 0 {
		return out, nil
	}
	// Read only the selected instance's extra fields. No list request reads raw
	// rows or extras, and oversized values are safely bounded without failing the whole container.
	extraRows, err := s.router.db.QueryContext(ctx, "SELECT extra_key,LEFT(extra_value,65536) FROM "+extra+" WHERE "+baseWhere+" AND entry_key=? AND BINARY entry_key=? ORDER BY extra_key LIMIT 1024", append(baseArgs, out[0].Key, out[0].Key)...)
	if err != nil {
		return nil, err
	}
	defer extraRows.Close()
	for extraRows.Next() {
		var k, v string
		if err = extraRows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if len(out[0].Stack.Extra) >= 1024 {
			break
		}
		if out[0].Stack.Extra == nil {
			out[0].Stack.Extra = map[string]string{}
		}
		out[0].Stack.Extra[k] = v
	}
	return out, extraRows.Err()
}

func (s *mysqlCharStore) InventoryBatch(ctx context.Context, c repository.CharacterSummary, containers []string, limit int) (map[string][]repository.InventorySlot, error) {
	if limit < 1 || limit > 101 || len(containers) > 16 {
		return nil, repository.ErrResourceRange
	}
	out := make(map[string][]repository.InventorySlot, len(containers))
	type groupKey struct {
		owner      string
		collection string
		list       int
	}
	grouped := map[groupKey][]repository.ContainerSpec{}
	var groupOrder []groupKey
	var standalone []string
	seen := map[string]bool{}
	for _, id := range containers {
		if seen[id] {
			continue
		}
		seen[id] = true
		spec, ok := repository.ResourceContainer(id)
		if !ok {
			return nil, repository.ErrResourceRange
		}
		out[id] = []repository.InventorySlot{}
		if id != "shared" && spec.Owner != "equipment" && spec.Last-spec.First <= 1024 {
			k := groupKey{owner: spec.Owner, collection: spec.Collection, list: spec.List}
			if _, exists := grouped[k]; !exists {
				groupOrder = append(groupOrder, k)
			}
			grouped[k] = append(grouped[k], spec)
		} else {
			standalone = append(standalone, id)
		}
	}
	for _, k := range groupOrder {
		specs := grouped[k]
		if len(specs) == 1 {
			rows, err := s.inventoryPage(ctx, c, specs[0].ID, -1, limit, false)
			if err != nil {
				return nil, err
			}
			out[specs[0].ID] = rows
			continue
		}
		owner, column := c.CharacterID, "character_id"
		itemsName := mysqlInventoryItemsTable
		if k.owner == "account" {
			owner, column = c.AccountID, "account_id"
			itemsName = mysqlAccountInventoryItemsTable
		}
		if owner == "" {
			return nil, repository.ErrResourceRange
		}
		itemsTable, err := s.router.readTable(itemsName, owner)
		if err != nil {
			return nil, err
		}
		minSlot, maxSlot := specs[0].First, specs[0].Last
		for _, sp := range specs[1:] {
			if sp.First < minSlot {
				minSlot = sp.First
			}
			if sp.Last > maxSlot {
				maxSlot = sp.Last
			}
		}
		slotExpr := "CAST(SUBSTRING_INDEX(entry_key,':',-1) AS UNSIGNED)"
		where := column + "=? AND BINARY " + column + "=?"
		args := []any{owner, owner}
		if k.collection != "" {
			where += " AND collection_name=?"
			args = append(args, k.collection)
		}
		where += " AND entry_key REGEXP ? AND " + slotExpr + " BETWEEN ? AND ? AND item_id>0"
		args = append(args, fmt.Sprintf("^%d:(0|[1-9][0-9]*)$", k.list), minSlot, maxSlot)
		rowCap := (maxSlot - minSlot + 1) * 2
		if rowCap < 1024 {
			rowCap = 1024
		}
		args = append(args, rowCap)
		query := "SELECT entry_key," + slotExpr + ",item_id,item_count,bind_flag,expire_at FROM " + itemsTable + " WHERE " + where + " ORDER BY " + slotExpr + ",BINARY entry_key LIMIT ?"
		rows, err := s.router.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row repository.InventorySlot
			var expiry sql.NullTime
			if err = rows.Scan(&row.Key, &row.Slot, &row.Stack.ItemID, &row.Stack.Count, &row.Stack.Bind, &expiry); err != nil {
				rows.Close()
				return nil, err
			}
			row.Stack.ExpireAt = scanTime(expiry)
			for _, sp := range specs {
				if row.Slot >= sp.First && row.Slot <= sp.Last {
					cur := out[sp.ID]
					if len(cur) > 0 && cur[len(cur)-1].Slot == row.Slot {
						break
					}
					if len(cur) < limit {
						out[sp.ID] = append(cur, row)
					}
					break
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, id := range standalone {
		rows, err := s.inventoryPage(ctx, c, id, -1, limit, false)
		if err != nil {
			return nil, err
		}
		out[id] = rows
	}
	return out, nil
}

func (s *mysqlCharStore) MailboxPage(ctx context.Context, id, after string, limit int) ([]repository.MailRecord, error) {
	if limit < 1 || limit > 101 || len(after) > 128 {
		return nil, repository.ErrResourceRange
	}
	table, err := s.router.readTable(mysqlMailsTable, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.router.db.QueryContext(ctx, "SELECT mail_id,sender_name,title,gold,read_flag,claimed_flag,created_at,expire_at FROM "+table+" WHERE character_id=? AND BINARY character_id=? AND deleted_flag=0 AND BINARY mail_id>? ORDER BY BINARY mail_id LIMIT ?", id, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []repository.MailRecord{}
	for rows.Next() {
		var m repository.MailRecord
		var created, expiry sql.NullTime
		if err = rows.Scan(&m.MailID, &m.SenderName, &m.Title, &m.Gold, &m.Read, &m.Claimed, &created, &expiry); err != nil {
			return nil, err
		}
		m.CreatedAt = scanTime(created)
		m.ExpireAt = scanTime(expiry)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *mysqlCharStore) MailDetail(ctx context.Context, id, mailID string) (repository.MailRecord, bool, error) {
	snapshot, close, err := s.resourceSnapshot(ctx)
	if err != nil {
		return repository.MailRecord{}, false, err
	}
	defer close()
	return snapshot.mailDetail(ctx, id, mailID)
}

func (s *mysqlCharStore) mailDetail(ctx context.Context, id, mailID string) (repository.MailRecord, bool, error) {
	var m repository.MailRecord
	table, err := s.router.readTable(mysqlMailsTable, id)
	if err != nil {
		return m, false, err
	}
	var created, expiry sql.NullTime
	err = s.router.db.QueryRowContext(ctx, "SELECT mail_id,sender_name,title,LEFT(body,262144),gold,read_flag,claimed_flag,created_at,expire_at FROM "+table+" WHERE character_id=? AND BINARY character_id=? AND mail_id=? AND BINARY mail_id=? AND deleted_flag=0", id, id, mailID, mailID).Scan(&m.MailID, &m.SenderName, &m.Title, &m.Body, &m.Gold, &m.Read, &m.Claimed, &created, &expiry)
	if err == sql.ErrNoRows {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	m.CreatedAt = scanTime(created)
	m.ExpireAt = scanTime(expiry)
	attachments, err := s.router.readTable(mysqlMailAttachmentsTable, id)
	if err != nil {
		return m, false, err
	}
	rows, err := s.router.db.QueryContext(ctx, "SELECT item_id,item_count,bind_flag,expire_at FROM "+attachments+" WHERE character_id=? AND BINARY character_id=? AND mail_id=? AND BINARY mail_id=? ORDER BY attachment_index LIMIT 500", id, id, mailID, mailID)
	if err != nil {
		return m, false, err
	}
	defer rows.Close()
	m.Attachments = []repository.MailAttachment{}
	for rows.Next() {
		var a repository.MailAttachment
		var expires sql.NullTime
		if err = rows.Scan(&a.ItemID, &a.Count, &a.Bind, &expires); err != nil {
			return m, false, err
		}
		a.ExpireAt = scanTime(expires)
		m.Attachments = append(m.Attachments, a)
	}
	return m, true, rows.Err()
}

func (s *mysqlCharStore) QuestPage(ctx context.Context, id, state string, after int64, limit int) ([]repository.CharacterQuest, error) {
	if limit < 1 || limit > 101 || after < 0 {
		return nil, repository.ErrResourceRange
	}
	table, err := s.router.readTable(mysqlQuestStatesTable, id)
	if err != nil {
		return nil, err
	}
	where := "q.character_id=? AND BINARY q.character_id=? AND q.quest_id>? AND (q.state_group='states' OR (q.state_group='progress' AND NOT EXISTS (SELECT 1 FROM " + table + " x WHERE x.character_id=q.character_id AND BINARY x.character_id=BINARY q.character_id AND x.quest_id=q.quest_id AND x.state_group='states')))"
	normalized := "LOWER(REPLACE(REPLACE(REPLACE(TRIM(q.status),'_',''),'-',''),' ',''))"
	switch state {
	case "active":
		where += " AND " + normalized + " IN ('active','accepted','inprogress','progress')"
	case "completed":
		where += " AND " + normalized + " IN ('complete','completed','cleared','finished','done','rewarded')"
	case "all":
	default:
		return nil, repository.ErrResourceRange
	}
	rows, err := s.router.db.QueryContext(ctx, "SELECT q.quest_id,q.status,q.trigger_type,q.progress_value,q.reward_select_index,q.multiplier,q.state_updated_at FROM "+table+" q WHERE "+where+" ORDER BY q.quest_id LIMIT ?", id, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []repository.CharacterQuest{}
	for rows.Next() {
		var q repository.CharacterQuest
		var updated sql.NullTime
		if err = rows.Scan(&q.ID, &q.State.Status, &q.State.TriggerType, &q.State.ProgressValue, &q.State.RewardSelectIndex, &q.State.Multiplier, &updated); err != nil {
			return nil, err
		}
		q.State.UpdatedAt = scanTime(updated)
		out = append(out, q)
	}
	return out, rows.Err()
}
