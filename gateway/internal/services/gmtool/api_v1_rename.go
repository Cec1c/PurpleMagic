package gmtool

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/encoding/simplifiedchinese"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1RenameOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID    string `json:"accountId"`
		ExpectedName string `json:"expectedName"`
		NewName      string `json:"newName"`
		Reason       string `json:"reason"`
	}
	if !v1Decode(w, r, &body) {
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
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	if c.AccountID != body.AccountID {
		v1Fail(w, r, 409, "owner_conflict", "角色所属账号已变更，请重新打开角色", false)
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	u, ok := g.Character.(repository.CharacterRenameUnitOfWork)
	if !ok || !s.characterRenameEnabled() {
		v1Fail(w, r, 503, "rename_unavailable", "当前服务未启用角色改名", false)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:min(12, len(s.cfg.LoginKeySHA256))]
	}
	command := adminops.RenameCommand{AccountID: c.AccountID, CharacterID: c.CharacterID, ExpectedName: body.ExpectedName, NewName: strings.TrimSpace(body.NewName), Reason: body.Reason, Actor: actor, Key: key}
	result, e := adminops.RunRenameOperation(r.Context(), u, command, commit, s.v1CharacterEditCheck(c.AccountID))
	switch {
	case e == nil:
		if !commit {
			v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result}})
		} else {
			v1Reply(w, r, 200, v1Envelope{Data: result})
		}
	case errors.Is(e, adminops.ErrRenameInput):
		v1Fail(w, r, 422, "invalid_character_name", characterNameErrorMessage(command.NewName), false)
	case errors.Is(e, repository.ErrCharacterNameTaken):
		v1Fail(w, r, 409, "name_taken", "角色名已被使用，请换一个名字", false)
	case errors.Is(e, adminops.ErrConflict):
		v1Fail(w, r, 409, "name_conflict", "角色名已变化，请刷新后重试", false)
	default:
		s.v1SkillPointResult(w, r, adminops.SkillPointReceipt{}, e)
	}
}

func characterNameErrorMessage(name string) string {
	if name == "" {
		return "请填写新角色名"
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return "角色名不能包含换行或控制字符"
		}
	}
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(name))
	if err != nil {
		return "角色名包含游戏不支持的字符，请使用 GBK 可编码的文字，去掉表情或生僻字"
	}
	if len(encoded) < 2 || len(encoded) > 12 {
		return fmt.Sprintf("角色名长度为 %d 字节，游戏允许 2–12 字节（一个汉字占 2 字节，字母或数字占 1 字节）", len(encoded))
	}
	return "改名请求内容无效，请重新提交"
}

func (s *Server) v1RenameLog(w http.ResponseWriter, r *http.Request) {
	q, e := v1ParseQuery(r)
	if e != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "改名记录查询参数无效", false)
		return
	}
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	u, ok := g.Character.(repository.CharacterRenameUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID)
	raw, e := s.v1CursorAfter(q.Cursor, "rename-log", filter)
	var before int64
	if e == nil && raw != "" {
		before, e = strconv.ParseInt(raw, 10, 64)
	}
	if e != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "改名记录分页条件已变更", false)
		return
	}
	rows, e := u.CharacterRenameLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("rename-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.RenameReceipt{}
	for _, row := range rows {
		v, e := adminops.DecodeRenameReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, v)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
