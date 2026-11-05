package gmtool

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1MailboxOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID        string                   `json:"accountId"`
		Items            []adminops.MailSelection `json:"items"`
		DiscardUnclaimed bool                     `json:"discardUnclaimed"`
		Reason           string                   `json:"reason"`
		PlanToken        string                   `json:"planToken"`
	}
	if !v1DecodeLimit(w, r, &body, 32<<10) {
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
		v1Fail(w, r, 409, "owner_conflict", "角色归属已变更，请重新打开角色", false)
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	u, ok := g.Character.(repository.MailboxManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:12]
	}
	command := adminops.MailboxCommand{AccountID: c.AccountID, CharacterID: c.CharacterID, Items: body.Items, DiscardUnclaimed: body.DiscardUnclaimed, Reason: body.Reason, Actor: actor, PlanToken: body.PlanToken, Key: key}
	if commit {
		old, found, e := adminops.LookupMailboxOperation(r.Context(), u, command)
		if found || e != nil {
			s.v1MailboxOperationResult(w, r, old, e)
			return
		}
	}
	identity := command
	identity.PlanToken = ""
	if commit {
		raw, e := s.v1CursorAfter(command.PlanToken, "mailbox-plan", adminops.MailboxIdentity(identity))
		at, parseErr := strconv.ParseInt(raw, 10, 64)
		age := time.Since(time.Unix(at, 0))
		if e != nil || parseErr != nil || age < 0 || age > 10*time.Minute {
			v1Fail(w, r, 409, "plan_conflict", "邮件预览已过期或内容已变更，请重新预览", false)
			return
		}
	} else {
		command.PlanToken = ""
	}
	result, e := adminops.RunMailboxOperation(r.Context(), u, command, commit, s.v1CharacterEditCheck(command.AccountID))
	if e == nil && !commit {
		items := make([]adminops.MailSelection, len(result.Changes))
		for i, v := range result.Changes {
			items[i] = adminops.MailSelection{MailID: v.MailID, Revision: v.Revision}
		}
		identity.Items = items
		token := s.v1CursorToken("mailbox-plan", adminops.MailboxIdentity(identity), strconv.FormatInt(time.Now().Unix(), 10))
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result, "items": items, "planToken": token}})
		return
	}
	s.v1MailboxOperationResult(w, r, result, e)
}
func (s *Server) v1MailboxOperationResult(w http.ResponseWriter, r *http.Request, result adminops.MailboxReceipt, e error) {
	switch {
	case e == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(e, adminops.ErrConflict):
		v1Fail(w, r, 409, "revision_conflict", "邮箱或邮件状态已变更，请刷新后重新预览", false)
	case errors.Is(e, adminops.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(e, adminops.ErrCharacterOnline):
		v1Fail(w, r, 409, "character_online", "角色在线；到系统设置里打开“碳铀模式”来强制数据写入", false)
	case errors.Is(e, adminops.ErrRuntimeUnavailable):
		v1Fail(w, r, 503, "runtime_unavailable", "暂时无法取得账号编辑状态", true)
	case errors.Is(e, adminops.ErrMailboxInput):
		v1Fail(w, r, 422, "invalid_mailbox_operation", "请选择1至100封邮件并填写删除原因", false)
	case errors.Is(e, adminops.ErrUnclaimedMail):
		v1Fail(w, r, 409, "unclaimed_assets", "选中邮件含未领取资产；勾选丢弃未领取资产后可重新预览", false)
	case errors.Is(e, repository.ErrManagedMailMissing):
		v1Fail(w, r, 409, "mail_missing", "选中邮件不存在或已删除，请刷新邮箱", false)
	case errors.Is(e, adminops.ErrAccount) || errors.Is(e, sql.ErrNoRows):
		v1Fail(w, r, 404, "character_not_found", "角色不存在或归属已变更", false)
	default:
		s.v1Unavailable(w, r, e)
	}
}
func (s *Server) v1MailboxOperationLog(w http.ResponseWriter, r *http.Request) {
	q, e := v1ParseQuery(r)
	if e != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "邮件流水查询参数无效", false)
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
	u, ok := g.Character.(repository.MailboxManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.AccountID+"\x00"+c.CharacterID)
	raw, e := s.v1CursorAfter(q.Cursor, "mailbox-log", filter)
	var before int64
	if e == nil && raw != "" {
		before, e = strconv.ParseInt(raw, 10, 64)
	}
	if e != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "邮件流水分页条件已变更", false)
		return
	}
	rows, e := u.MailboxManagementLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("mailbox-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.MailboxReceipt{}
	for _, row := range rows {
		v, e := adminops.DecodeMailboxReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, v)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
