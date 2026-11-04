package gmtool

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1AdventureName(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AdventureRenameUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	state, err := u.LoadAdventureName(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		v1Fail(w, r, 404, "account_not_found", "账号存档不存在", false)
		return
	}
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: state})
}

func (s *Server) v1AdventureRenameOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedName *string `json:"expectedName"`
		NewName      string  `json:"newName"`
		Reason       string  `json:"reason"`
	}
	if !v1Decode(w, r, &body) {
		return
	}
	if body.ExpectedName == nil {
		v1Fail(w, r, 400, "expected_name_required", "请提供当前冒险团名", false)
		return
	}
	commit := !strings.HasSuffix(r.URL.Path, "/preview")
	key := ""
	if commit {
		var ok bool
		key, ok = v1Idempotency(w, r)
		if !ok {
			return
		}
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AdventureRenameUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:min(12, len(s.cfg.LoginKeySHA256))]
	}
	c := adminops.AdventureRenameCommand{AccountID: id, ExpectedName: *body.ExpectedName, NewName: strings.TrimSpace(body.NewName), Reason: body.Reason, Actor: actor, Key: key}
	result, err := adminops.RunAdventureRename(r.Context(), u, c, commit, s.v1AccountEditCheck(id))
	switch {
	case err == nil:
		if commit {
			v1Reply(w, r, 200, v1Envelope{Data: result})
		} else {
			v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result}})
		}
	case errors.Is(err, adminops.ErrAdventureNameInput):
		v1Fail(w, r, 422, "invalid_adventure_name", "冒险团名须为 1–16 个 GB18030 字节（最多 8 个常用汉字），不能包含空白或控制字符", false)
	case errors.Is(err, repository.ErrRepresentAccountNameExists):
		v1Fail(w, r, 409, "name_taken", "冒险团名已被使用，请换一个名字", false)
	case errors.Is(err, adminops.ErrAdventureNotRegistered):
		v1Fail(w, r, 409, "adventure_not_registered", "请先在游戏内创建冒险团", false)
	case errors.Is(err, adminops.ErrConflict):
		v1Fail(w, r, 409, "name_conflict", "冒险团名已变化，请刷新后重试", false)
	case errors.Is(err, adminops.ErrCharacterOnline):
		v1Fail(w, r, 409, "account_online", "请先退出该账号的游戏连接，再修改冒险团名", false)
	case errors.Is(err, sql.ErrNoRows):
		v1Fail(w, r, 404, "account_not_found", "账号存档不存在", false)
	default:
		s.v1SkillPointResult(w, r, adminops.SkillPointReceipt{}, err)
	}
}

func (s *Server) v1AdventureRenameLog(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "冒险团改名记录查询参数无效", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AdventureRenameUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, id)
	raw, err := s.v1CursorAfter(q.Cursor, "adventure-rename-log", filter)
	var before int64
	if err == nil && raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "冒险团改名记录分页条件已变更", false)
		return
	}
	rows, err := u.AdventureRenameLog(r.Context(), id, before, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("adventure-rename-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.AdventureRenameReceipt{}
	for _, row := range rows {
		v, e := adminops.DecodeAdventureRenameReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, v)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
