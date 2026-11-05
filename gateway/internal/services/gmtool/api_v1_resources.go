package gmtool

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	dnfquest "dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1ResourceCharacter(w http.ResponseWriter, r *http.Request) (repository.CharacterSummary, repository.CharacterResourceReader, bool) {
	var c repository.CharacterSummary
	id := r.PathValue("characterId")
	if !v1ValidID(id) {
		v1Fail(w, r, 400, "invalid_query", "角色标识无效", false)
		return c, nil, false
	}
	reader, err := s.v1SummaryReader()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return c, nil, false
	}
	c, found, err := reader.FindSummary(r.Context(), id)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return c, nil, false
	}
	if !found {
		v1Fail(w, r, 404, "character_not_found", "角色不存在或已删除", false)
		return c, nil, false
	}
	resources, ok := reader.(repository.CharacterResourceReader)
	if !ok {
		s.v1Unavailable(w, r, errors.New("resource reader unavailable"))
		return c, nil, false
	}
	return c, resources, true
}
func (s *Server) v1PVFChecksum() string {
	if s.deps.PVF == nil {
		return ""
	}
	source, err := s.source()
	if err != nil {
		return ""
	}
	return itemIndexSourceChecksum(source)
}
func v1Time(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func (s *Server) v1RequireItemIndex(w http.ResponseWriter, r *http.Request) bool {
	if s.deps.Catalog != nil {
        _, e := s.deps.Catalog.metadata(r.Context())
		s.items.mu.Lock()
		s.items.buildEr = e
		s.items.mu.Unlock()
	}
	if s.items == nil || !s.items.isReady() || s.items.failure() != nil {
		v1Fail(w, r, 503, "item_index_unavailable", "当前 PVF 的物品索引尚未准备好", true)
		return false
	}
	return true
}

func (s *Server) v1Inventory(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "container")
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "背包查询参数无效", false)
		return
	}
	container := q.Values.Get("container")
	if _, ok := repository.ResourceContainer(container); !ok {
		v1Fail(w, r, 400, "invalid_container", "请选择物品容器", false)
		return
	}
	c, reader, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	if !s.v1RequireItemIndex(w, r) {
		return
	}
	checksum := s.v1PVFChecksum()
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID+"\x00"+container+"\x00"+checksum)
	after, err := s.v1CursorAfter(q.Cursor, "inventory", filter)
	slot := -1
	if err == nil && after != "" {
		slot, err = strconv.Atoi(after)
	}
	if err != nil || slot < -1 || slot > 32767 {
		v1Fail(w, r, 400, "invalid_cursor", "分页条件或 PVF 已变更，请刷新", false)
		return
	}
	rows, err := reader.InventoryPage(r.Context(), c, container, slot, q.Limit+1, false)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	var present map[int]bool
	if container == "shared" {
		rows, present = v1SharedResourcePage(rows, slot, q.Limit+1)
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("inventory", filter, strconv.Itoa(rows[len(rows)-1].Slot))
	}
	if s.deps.Catalog != nil {
		ids := []uint32{}
		for _, row := range rows {
			if row.Stack.ItemID > 0 && row.Stack.ItemID <= math.MaxUint32 {
				ids = append(ids, uint32(row.Stack.ItemID))
			}
		}
		if e := s.deps.Catalog.preload(r.Context(), ids); e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
	}
	data := []map[string]any{}
	for _, row := range rows {
		item := s.v1InventoryRow(container, row, true)
		item["revision"] = adminops.InventorySlotRevision(c.AccountID, c.CharacterID, container, row.Slot, row.Stack, container != "shared" || present[row.Slot])
		// The directory query deliberately omits raw bytes and extra fields.
		// Its digest describes the displayed summary, not the full write revision.
		item["revisionScope"] = "summary"
		if container == "shared" {
			item["present"] = present[row.Slot]
		}
		data = append(data, item)
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"characterId": c.CharacterID, "accountId": c.AccountID, "container": container, "containers": repository.ResourceContainers(), "items": data, "pvfChecksum": checksum}, Page: &page})
}

func (s *Server) v1InventoryDetail(w http.ResponseWriter, r *http.Request) {
	container := r.PathValue("container")
	spec, valid := repository.ResourceContainer(container)
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if !valid || err != nil || slot < spec.First || slot > spec.Last || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "物品位置无效", false)
		return
	}
	c, reader, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	if !s.v1RequireItemIndex(w, r) {
		return
	}
	rows, err := reader.InventoryPage(r.Context(), c, container, slot, 1, true)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	row, present, found := v1SelectedInventoryRow(rows, container, slot)
	if !found {
		v1Fail(w, r, 404, "item_not_found", "该位置当前没有物品，请刷新列表", false)
		return
	}
	data := s.v1InventoryRow(container, row, true)
	data["present"] = present
	data["revision"] = adminops.InventorySlotRevision(c.AccountID, c.CharacterID, container, slot, row.Stack, present)
	data["pvfChecksum"] = s.v1PVFChecksum()
	if _, err = v1AttachPetState(r.Context(), reader, c, adminops.InventorySelection{Container: container, Slot: slot}, row.Stack, data); err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}

func (s *Server) v1InventoryRow(container string, row repository.InventorySlot, detail bool) map[string]any {
	stack := row.Stack
	var entry itemEntry
	found := false
	if s.items != nil && stack.ItemID > 0 && stack.ItemID <= math.MaxUint32 {
		entry, found = s.items.lookup(uint32(stack.ItemID))
	}
	out := map[string]any{"present": true, "slot": row.Slot, "container": container, "itemId": strconv.FormatInt(stack.ItemID, 10), "count": strconv.FormatInt(stack.Count, 10), "bound": stack.Bind, "expiresAt": v1Time(stack.ExpireAt), "definitionKnown": found, "name": entry.Name, "kind": entry.Kind, "rarity": entry.Rarity}
	out["tag"] = entry.Tag
	out["minimumLevel"] = entry.MinLevel
	if !detail {
		return out
	}
	raw, _ := json.Marshal(row)
	sum := sha256.Sum256(raw)
	out["revision"] = hex.EncodeToString(sum[:])
	// Decode raw offsets only for the current verified 0x77 item-row shape.
	if len(stack.RawEntry) != itemcfgItemListEntrySize {
		stack.RawEntry = nil
	}
	attributes := map[string]any{}
	// These readers share the existing server-compatible instance projection.
	// A field without a persisted source remains null rather than a fabricated 0.
	has := func(offset int, keys ...string) bool {
		if len(stack.RawEntry) > offset {
			return true
		}
		for _, k := range keys {
			if _, ok := stack.Extra[k]; ok {
				return true
			}
		}
		return false
	}
	put := func(name string, known bool, value any) {
		if known {
			attributes[name] = value
		} else {
			attributes[name] = nil
		}
	}
	if found && entry.Kind == "equipment" && !itemcfgIsAvatar(entry) && container != "avatars" {
		put("qualitySeed", has(9, "quality_seed"), strconv.FormatUint(uint64(itemcfgQualitySeed(stack, entry)), 10))
		put("reinforce", has(0x0a, "ext_data0", "ext0", "packed_flag_byte", "packed_flag", "packed", "reinforce", "upgrade_level", "enchant_upgrade", "enchant_upgrade_count"), itemcfgUpgradeLevelOf(stack))
		put("amplifyType", has(0x13, "amplify_type", "amplification_type", "amplify_attr", "amplification_attr", "byte_13", "value_13", "value_c"), itemcfgAmplifyTypeOf(stack))
		put("amplifyValue", has(0x15, "amplify_value", "amplification_value", "amplify_bonus", "amplification_bonus", "marker_16", "marker16", "value_d"), itemcfgAmplifyValueOf(stack))
		put("forging", has(itemcfgForgingRowOffset, itemcfgForgingExtraKeys...), itemcfgForgingLevelOf(stack))
	}
	for _, key := range []string{"durability", "max_durability", "seal", "serial", "enchant_id", "enchant_card_id", "avatar_option_value", "emblem_0", "emblem_1", "emblem_2"} {
		value, ok := stack.Extra[key]
		put(key, ok, value)
	}
	if attributes["durability"] == nil && len(stack.RawEntry) == itemcfgItemListEntrySize {
		attributes["durability"] = strconv.Itoa(int(binary.LittleEndian.Uint16(stack.RawEntry[0x0b:0x0d])))
	}
	if value, ok := stack.Extra["seal_flag"]; ok {
		attributes["seal"] = value
	} else if attributes["seal"] == nil && len(stack.RawEntry) == itemcfgItemListEntrySize {
		attributes["seal"] = strconv.Itoa(int(stack.RawEntry[0x0d]))
	}
	out["attributes"] = attributes
	return out
}

func (s *Server) v1Mailbox(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "sender", "claimed", "from", "to")
	f, filterErr := v1MailboxFilter(q)
	if err != nil || filterErr != nil {
		v1Fail(w, r, 400, "invalid_query", "邮箱查询参数无效", false)
		return
	}
	c, reader, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	filter := v1Filter(q, c.AccountID+"\x00"+c.CharacterID+"\x00"+v1MailboxFilterIdentity(f))
	after, err := s.v1CursorAfter(q.Cursor, "mailbox", filter)
	if err != nil {
		v1Fail(w, r, 400, "invalid_cursor", "邮箱分页游标无效，请刷新", false)
		return
	}
	filtered, available := reader.(repository.MailboxFilteredReader)
	if !available {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	rows, err := filtered.MailboxFilteredPage(r.Context(), c.CharacterID, f, after, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("mailbox", filter, rows[len(rows)-1].MailID)
	}
	data := []map[string]any{}
	for _, m := range rows {
		data = append(data, v1MailRow(m))
	}
	v1Reply(w, r, 200, v1Envelope{Data: data, Page: &page})
}
func v1MailRow(m repository.MailRecord) map[string]any {
	return map[string]any{"mailId": m.MailID, "sender": m.SenderName, "title": m.Title, "gold": strconv.FormatInt(m.Gold, 10), "read": m.Read, "claimed": m.Claimed, "createdAt": v1Time(m.CreatedAt), "expiresAt": v1Time(m.ExpireAt)}
}
func (s *Server) v1MailboxDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("mailId")
	if !v1ValidID(id) || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "邮件标识无效", false)
		return
	}
	c, reader, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	m, found, err := reader.MailDetail(r.Context(), c.CharacterID, id)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	if !found {
		v1Fail(w, r, 404, "mail_not_found", "邮件不存在或已删除", false)
		return
	}
	if s.deps.Catalog != nil {
		ids := []uint32{}
		for _, a := range m.Attachments {
			if a.ItemID > 0 && a.ItemID <= math.MaxUint32 {
				ids = append(ids, uint32(a.ItemID))
			}
		}
		if e := s.deps.Catalog.preload(r.Context(), ids); e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
	}
	out := v1MailRow(m)
	out["body"] = m.Body
	attachments := []map[string]any{}
	for i, a := range m.Attachments {
		attachments = append(attachments, s.v1InventoryRow("mail", repository.InventorySlot{Slot: i, Stack: repository.ItemStack{ItemID: a.ItemID, Count: a.Count, Bind: a.Bind, ExpireAt: a.ExpireAt}}, false))
	}
	out["attachments"] = attachments
	v1Reply(w, r, 200, v1Envelope{Data: out})
}

func (s *Server) v1QuestIndex(w http.ResponseWriter, r *http.Request) (*questDiskIndex, bool) {
	ix, err := s.questReadIndex(r.Context())
	if err != nil {
		v1Fail(w, r, 503, "quest_index_unavailable", "当前 PVF 的任务索引尚未准备好", true)
		return nil, false
	}
	return ix, true
}
func questIndexSummary(row questIndexRow) map[string]any {
	return map[string]any{"questId": strconv.FormatInt(row.ID, 10), "name": row.Name, "path": row.Path, "grade": row.Grade, "levelMin": row.LevelMin, "levelMax": row.LevelMax}
}
func (s *Server) v1CharacterQuests(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "state")
	state := q.Values.Get("state")
	if state == "" {
		state = "active"
	}
	if err != nil || q.Q != "" || (state != "active" && state != "completed" && state != "all") {
		v1Fail(w, r, 400, "invalid_query", "请选择进行中、已完成或全部任务", false)
		return
	}
	c, reader, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	ix, ok := s.v1QuestIndex(w, r)
	if !ok {
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+state+"\x00"+ix.Checksum+"\x00"+ix.ManifestHash)
	after, err := s.v1CursorAfter(q.Cursor, "character-quests", filter)
	id := int64(0)
	if err == nil && after != "" {
		id, err = strconv.ParseInt(after, 10, 64)
	}
	if err != nil || id < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "任务分页条件或 PVF 已变更，请刷新", false)
		return
	}
	rows, err := reader.QuestPage(r.Context(), c.CharacterID, state, id, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("character-quests", filter, strconv.FormatInt(rows[len(rows)-1].ID, 10))
	}
	data := []map[string]any{}
	if ix.remote != nil && len(rows) > 0 {
		ids := make([]int64, 0, len(rows))
		for _, q := range rows {
			ids = append(ids, q.ID)
		}
		if e := ix.remoteLoad(r.Context(), ids); e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
	}
	for _, q := range rows {
		row, known := ix.find(q.ID)
		row.ID = q.ID
		out := questIndexSummary(row)
		out["definitionKnown"] = known
		out["status"] = q.State.Status
		out["progress"] = strconv.FormatInt(q.State.ProgressValue, 10)
		out["updatedAt"] = v1Time(q.State.UpdatedAt)
		data = append(data, out)
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"quests": data, "pvfChecksum": ix.Checksum}, Page: &page})
}
func (s *Server) v1Quests(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "kind")
	if err != nil {
		v1Fail(w, r, 400, "invalid_query", "任务库查询参数无效", false)
		return
	}
	ix, ok := s.v1QuestIndex(w, r)
	if !ok {
		return
	}
	filter := v1Filter(q, q.Values.Get("kind")+"\x00"+ix.Checksum+"\x00"+ix.ManifestHash)
	after, err := s.v1CursorAfter(q.Cursor, "quests", filter)
	id := int64(0)
	if err == nil && after != "" {
		id, err = strconv.ParseInt(after, 10, 64)
	}
	if err != nil || id < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "任务库或查询条件已变更，请刷新", false)
		return
	}
	data := []map[string]any{}
	page := v1Page{Limit: q.Limit}
	query := strings.ToLower(q.Q)
	last := int64(0)
	for _, row := range ix.Rows {
		if err = r.Context().Err(); err != nil {
			s.v1Unavailable(w, r, err)
			return
		}
		if row.ID <= id || (q.Values.Get("kind") != "" && q.Values.Get("kind") != row.Grade) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Name+" "+row.Path), query) && strconv.FormatInt(row.ID, 10) != query {
			continue
		}
		if len(data) == q.Limit {
			page.NextCursor = s.v1CursorToken("quests", filter, strconv.FormatInt(last, 10))
			break
		}
		data = append(data, questIndexSummary(row))
		last = row.ID
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"quests": data, "pvfChecksum": ix.Checksum}, Page: &page})
}
func (s *Server) v1QuestDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("questId"), 10, 32)
	if err != nil || id <= 0 || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "任务编号无效", false)
		return
	}
	ix, ok := s.v1QuestIndex(w, r)
	if !ok {
		return
	}
	d, err := ix.definition(r.Context(), id)
	if errors.Is(err, os.ErrNotExist) {
		v1Fail(w, r, 404, "quest_not_found", "当前 PVF 未收录此任务", false)
		return
	}
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	row, _ := ix.find(id)
	data := questIndexSummary(row)
	data["pvfChecksum"] = ix.Checksum
	data["dependMessage"] = d.DependMessage
	data["conditionMessage"] = d.ConditionMessage
	data["solveMessage"] = d.SolveMessage
	data["type"] = d.Type
	data["job"] = d.Job
	data["rewardType"] = d.RewardType
	data["rewardParseError"] = d.RewardParseError
	data["checkCount"] = v1Ints(d.CheckCount)
	data["rewardData"] = v1Ints(d.RewardIntData)
	groups := [][]string{}
	for _, g := range d.PreRequiredGroups {
		groups = append(groups, v1Ints(g))
	}
	data["prerequisites"] = groups
	data["rewards"] = s.v1QuestRewards(d.RewardItems)
	data["selectionRewards"] = s.v1QuestRewards(d.RewardSelectionItems)
	v1Reply(w, r, 200, v1Envelope{Data: data})
}
func v1Ints(values []int64) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strconv.FormatInt(v, 10))
	}
	return out
}
func (s *Server) v1QuestRewards(values []dnfquest.RewardItemRule) []map[string]any {
	out := []map[string]any{}
	for _, v := range values {
		out = append(out, map[string]any{"itemId": strconv.FormatInt(v.ItemID, 10), "count": strconv.FormatInt(v.Count, 10), "jobFiltered": v.HasJobFilter, "job": v.Job, "growType": v.GrowType})
	}
	return out
}
