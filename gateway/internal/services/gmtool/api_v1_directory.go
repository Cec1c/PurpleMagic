package gmtool

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

type v1AccountRow struct {
	PanelID        string     `json:"panelId"`
	AccountID      string     `json:"accountId"`
	Name           string     `json:"name"`
	Disabled       bool       `json:"disabled"`
	CreatedAt      *time.Time `json:"createdAt"`
	LastLoginAt    *time.Time `json:"lastLoginAt"`
	CharacterCount int        `json:"characterCount"`
}

func scanV1Account(row interface{ Scan(...any) error }) (v1AccountRow, error) {
	var out v1AccountRow
	var panelID int64
	var created, login sql.NullTime
	err := row.Scan(&panelID, &out.AccountID, &out.Name, &out.Disabled, &created, &login)
	out.PanelID = strconv.FormatInt(panelID, 10)
	if created.Valid {
		v := created.Time.UTC()
		out.CreatedAt = &v
	}
	if login.Valid {
		v := login.Time.UTC()
		out.LastLoginAt = &v
	}
	return out, err
}
func (s *Server) v1AccountSelect() string {
	return "SELECT a.id,a.account_id,a.account,COALESCE(p.disabled,0),a.created_at,a.last_login_at FROM " + quoteIdentifier(s.cfg.Table) + " a LEFT JOIN " + quoteIdentifier(s.cfg.Table+"_player_accounts") + " p ON p.account_id=BINARY a.account_id"
}
func (s *Server) v1LoadAccount(ctx context.Context, id string) (v1AccountRow, error) {
	if s.db == nil {
		return v1AccountRow{}, errors.New("registration database unavailable")
	}
	return scanV1Account(s.db.QueryRowContext(ctx, s.v1AccountSelect()+" WHERE a.account_id=? AND BINARY a.account_id=?", id, id))
}
func (s *Server) v1Accounts(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "state")
	if err != nil {
		v1Fail(w, r, 400, "invalid_query", "查询参数无效，分页数量须为 1 至 100", false)
		return
	}
	state := q.Values.Get("state")
	if state != "" && state != "enabled" && state != "disabled" {
		v1Fail(w, r, 400, "invalid_query", "账号状态筛选无效", false)
		return
	}
	filter := v1Filter(q, state)
	after, err := s.v1CursorAfter(q.Cursor, "accounts", filter)
	var id int64
	if err == nil && after != "" {
		id, err = strconv.ParseInt(after, 10, 64)
	}
	if err != nil || id < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "分页游标无效或筛选已变更，请重新查询", false)
		return
	}
	reader, err := s.v1SummaryReader()
	if err != nil || s.db == nil {
		s.v1Unavailable(w, r, errors.New("directory unavailable"))
		return
	}
	query := s.v1AccountSelect() + " WHERE a.id>?"
	args := []any{id}
	if q.Q != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(q.Q) + "%"
		query += " AND (a.account LIKE ? ESCAPE '!' OR a.account_id LIKE ? ESCAPE '!' OR a.id=?)"
		panel, _ := strconv.ParseInt(q.Q, 10, 64)
		args = append(args, pattern, pattern, panel)
	}
	if state != "" {
		query += " AND COALESCE(p.disabled,0)=?"
		args = append(args, state == "disabled")
	}
	query += " ORDER BY a.id LIMIT ?"
	args = append(args, q.Limit+1)
	rows, err := diagnostics.Query(r.Context(), s.db, "accounts.registration", query, args...)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	accounts := []v1AccountRow{}
	for rows.Next() {
		item, e := scanV1Account(rows)
		if e != nil {
			rows.Close()
			s.v1Unavailable(w, r, e)
			return
		}
		accounts = append(accounts, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(accounts) > q.Limit {
		accounts = accounts[:q.Limit]
		page.NextCursor = s.v1CursorToken("accounts", filter, accounts[len(accounts)-1].PanelID)
	}
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.AccountID)
	}
	summaryDone := diagnostics.Start(r.Context(), "accounts.characters")
	characters, err := reader.SummariesForAccounts(r.Context(), ids)
	summaryDone(err)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	counts := map[string]int{}
	assemblyDone := diagnostics.Start(r.Context(), "accounts.assembly")
	for _, c := range characters {
		counts[c.AccountID]++
	}
	for i := range accounts {
		accounts[i].CharacterCount = counts[accounts[i].AccountID]
	}
	assemblyDone(nil)
	v1Reply(w, r, 200, v1Envelope{Data: accounts, Page: &page})
}

func (s *Server) v1AccountData(w http.ResponseWriter, r *http.Request) (v1AccountRow, []repository.CharacterSummary, bool) {
	id := r.PathValue("accountId")
	if !v1ValidID(id) || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "账号标识或查询参数无效", false)
		return v1AccountRow{}, nil, false
	}
	a, err := s.v1LoadAccount(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		v1Fail(w, r, 404, "account_not_found", "账号不存在", false)
		return a, nil, false
	}
	if err != nil {
		s.v1Unavailable(w, r, err)
		return a, nil, false
	}
	reader, err := s.v1SummaryReader()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return a, nil, false
	}
	characters, err := reader.SummariesForAccounts(r.Context(), []string{a.AccountID})
	if err != nil {
		s.v1Unavailable(w, r, err)
		return a, nil, false
	}
	a.CharacterCount = len(characters)
	return a, characters, true
}
func (s *Server) v1Account(w http.ResponseWriter, r *http.Request) {
	a, _, ok := s.v1AccountData(w, r)
	if !ok {
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: a})
}
func (s *Server) v1AccountCharacters(w http.ResponseWriter, r *http.Request) {
	_, characters, ok := s.v1AccountData(w, r)
	if !ok {
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: characters})
}
func (s *Server) v1Characters(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "online")
	if err != nil {
		v1Fail(w, r, 400, "invalid_query", "角色查询参数无效", false)
		return
	}
	status := q.Values.Get("online")
	if status != "" && status != "online" && status != "offline" {
		v1Fail(w, r, 400, "invalid_query", "在线状态筛选无效", false)
		return
	}
	filter := v1Filter(q, status)
	after, err := s.v1CursorAfter(q.Cursor, "characters", filter)
	if err != nil {
		v1Fail(w, r, 400, "invalid_cursor", "分页游标无效或筛选已变更，请重新查询", false)
		return
	}
	reader, err := s.v1SummaryReader()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	var characters []repository.CharacterSummary
	if status == "" {
		characters, err = reader.SearchSummaries(r.Context(), q.Q, after, q.Limit+1)
	} else {
		presenceReader, ok := reader.(interface {
			SearchSummariesByPresence(context.Context, string, string, int, []string, bool) ([]repository.CharacterSummary, error)
		})
		if !ok || s.deps.Runtime == nil {
			v1Fail(w, r, 503, "presence_unavailable", "在线状态暂时无法读取", true)
			return
		}
		snapshot, e := s.deps.Runtime.OnlineSnapshot(r.Context())
		if e != nil {
			v1Fail(w, r, 503, "presence_unavailable", "在线状态暂时无法读取", true)
			return
		}
		ids := make([]string, 0, len(snapshot.Members))
		for _, member := range snapshot.Members {
			ids = append(ids, strconv.FormatUint(uint64(member.CharacterID), 10))
		}
		characters, err = presenceReader.SearchSummariesByPresence(r.Context(), q.Q, after, q.Limit+1, ids, status == "online")
	}
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(characters) > q.Limit {
		characters = characters[:q.Limit]
		page.NextCursor = s.v1CursorToken("characters", filter, characters[len(characters)-1].CharacterID)
	}
	v1Reply(w, r, 200, v1Envelope{Data: characters, Page: &page})
}
func (s *Server) v1Character(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("characterId")
	if !v1ValidID(id) || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "角色标识或查询参数无效", false)
		return
	}
	reader, err := s.v1SummaryReader()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	c, found, err := reader.FindSummary(r.Context(), id)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	if !found {
		v1Fail(w, r, 404, "character_not_found", "角色不存在或已删除", false)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"character": c, "online": "unknown"}})
}
func (s *Server) v1Items(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r, "kind")
	if err != nil {
		v1Fail(w, r, 400, "invalid_query", "物品查询参数无效", false)
		return
	}
	kind := q.Values.Get("kind")
	if kind != "" && kind != "stackable" && kind != "equipment" {
		v1Fail(w, r, 400, "invalid_query", "物品类型无效", false)
		return
	}
	filter := v1Filter(q, kind+"\x00"+s.v1PVFChecksum()+"\x00"+strconv.Itoa(itemIndexVersion))
	after, err := s.v1CursorAfter(q.Cursor, "items", filter)
	offset := 0
	if err == nil && after != "" {
		offset, err = strconv.Atoi(after)
	}
	if err != nil || offset < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "分页游标无效或筛选已变更，请重新查询", false)
		return
	}
	if s.items == nil || !s.items.isReady() || s.items.failure() != nil {
		v1Fail(w, r, 503, "item_index_unavailable", "物品目录尚未准备好，请稍后刷新", true)
		return
	}
	entries, total := s.items.search(itemSearchQuery{Q: q.Q, Kind: kind, Limit: q.Limit, Offset: offset, Rarity: -1, UsableJob: -1})
	data := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		data = append(data, map[string]any{"itemId": strconv.FormatUint(uint64(e.ID), 10), "name": e.Name, "kind": e.Kind, "rarity": e.Rarity, "minLevel": e.MinLevel, "category": e.Tag, "segment": e.Segment, "stackLimit": strconv.FormatInt(e.StackLimit, 10), "invalid": e.Invalid, "templateExpiration": templateExpirationPayload(e)})
	}
	page := v1Page{Limit: q.Limit}
	if offset+len(entries) < total {
		page.NextCursor = s.v1CursorToken("items", filter, strconv.Itoa(offset+len(entries)))
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"items": data, "total": total}, Page: &page})
}
