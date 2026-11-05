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

func (s *Server) v1Gold(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	g, err := s.repositories()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	reader, ok := g.Character.(repository.CharacterGoldReader)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	ch, found, err := reader.LoadCharacterGold(r.Context(), c.CharacterID)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	if !found || ch.AccountID != c.AccountID || ch.Stats["delete_flag"] != 0 {
		v1Fail(w, r, 404, "character_not_found", "角色不存在或归属已变更", false)
		return
	}
	balance, err := adminops.CharacterGoldBalance(ch)
	if err != nil {
		s.v1GoldResult(w, r, adminops.GoldReceipt{}, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"characterId": ch.CharacterID, "accountId": ch.AccountID, "balance": strconv.FormatInt(balance, 10), "maximum": strconv.FormatInt(adminops.MaxCharacterGold, 10), "revision": adminops.CharacterGoldRevision(ch)}})
}

func (s *Server) v1GoldOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID string `json:"accountId"`
		Action    string `json:"action"`
		Amount    string `json:"amount"`
		Revision  string `json:"revision"`
		Reason    string `json:"reason"`
		PlanToken string `json:"planToken"`
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
	g, err := s.repositories()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	u, ok := g.Character.(repository.CharacterGoldUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:12]
	}
	command := adminops.GoldCommand{AccountID: c.AccountID, CharacterID: c.CharacterID, Action: body.Action, Amount: body.Amount, Revision: body.Revision, Reason: body.Reason, Actor: actor, PlanToken: body.PlanToken, Key: key}
	if commit {
		old, found, e := adminops.LookupGoldOperation(r.Context(), u, command)
		if found || e != nil {
			s.v1GoldResult(w, r, old, e)
			return
		}
	}
	identity := command
	identity.PlanToken = ""
	fingerprint := adminops.GoldIdentity(identity)
	if commit {
		raw, e := s.v1CursorAfter(command.PlanToken, "gold-plan", fingerprint)
		at, parseErr := strconv.ParseInt(raw, 10, 64)
		age := time.Since(time.Unix(at, 0))
		if e != nil || parseErr != nil || age < 0 || age > 10*time.Minute {
			v1Fail(w, r, 409, "plan_conflict", "金币预览已过期或内容已变更，请重新预览", false)
			return
		}
	} else {
		command.PlanToken = ""
	}
	result, err := adminops.RunGoldOperation(r.Context(), u, command, commit, s.v1CharacterEditCheck(command.AccountID))
	if err == nil && !commit {
		token := s.v1CursorToken("gold-plan", fingerprint, strconv.FormatInt(time.Now().Unix(), 10))
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result, "planToken": token}})
		return
	}
	s.v1GoldResult(w, r, result, err)
}

func (s *Server) v1GoldResult(w http.ResponseWriter, r *http.Request, result adminops.GoldReceipt, err error) {
	switch {
	case err == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(err, adminops.ErrConflict):
		v1Fail(w, r, 409, "revision_conflict", "角色金币或资料已变更，请刷新后重新预览", false)
	case errors.Is(err, adminops.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(err, adminops.ErrCharacterOnline):
		v1Fail(w, r, 409, "character_online", "角色在线；到系统设置里打开“碳铀模式”来强制数据写入", false)
	case errors.Is(err, adminops.ErrRuntimeUnavailable):
		v1Fail(w, r, 503, "runtime_unavailable", "暂时无法取得账号编辑状态", true)
	case errors.Is(err, adminops.ErrGoldInput):
		v1Fail(w, r, 422, "invalid_amount", "请检查操作原因和金币数量，变更后的金币须在 0 至 2147483647 之间", false)
	case errors.Is(err, adminops.ErrGoldBalance):
		v1Fail(w, r, 422, "invalid_stored_balance", "角色金币存档超出范围或版本异常，请核对存档", false)
	case errors.Is(err, adminops.ErrAccount) || errors.Is(err, sql.ErrNoRows):
		v1Fail(w, r, 404, "character_not_found", "角色不存在或归属已变更", false)
	default:
		s.v1Unavailable(w, r, err)
	}
}

func (s *Server) v1GoldLog(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "金币流水查询参数无效", false)
		return
	}
	c, _, ok := s.v1ResourceCharacter(w, r)
	if !ok {
		return
	}
	g, err := s.repositories()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	u, ok := g.Character.(repository.CharacterGoldUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID)
	raw, err := s.v1CursorAfter(q.Cursor, "gold-log", filter)
	var before int64
	if err == nil && raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "金币流水分页条件已变更", false)
		return
	}
	rows, err := u.CharacterGoldLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("gold-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.GoldReceipt{}
	for _, row := range rows {
		item, e := adminops.DecodeGoldReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, item)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
