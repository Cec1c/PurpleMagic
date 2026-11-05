package gmtool

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/progression"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) skillPointDefinition(ctx context.Context, level int) (adminops.SkillPointDefinition, error) {
	if s.deps.Catalog != nil {
		var d adminops.SkillPointDefinition
		e := s.deps.Catalog.call(ctx, catalogRequest{Operation: "skill-points", Level: level}, &d)
		return d, e
	}
	var d adminops.SkillPointDefinition
	if s.pvf == nil || s.deps.PVF == nil {
		return d, errRepositoriesUnavailable
	}
	source, err := s.source()
	if err != nil {
		return d, err
	}
	hash := itemIndexSourceChecksum(source)
	if hash == "" {
		return d, errRepositoriesUnavailable
	}
	s.pvf.mu.Lock()
	defer s.pvf.mu.Unlock()
	if s.pvf.skillPointChecksum != hash || s.pvf.skillPointTables == nil {
		tables, e := progression.LoadSkillPointTable(ctx, source)
		if e != nil {
			return d, e
		}
		s.pvf.skillPointTables, s.pvf.skillPointChecksum = tables, hash
	}
	sp, err := s.pvf.skillPointTables.TotalSkillPoints(level)
	if err != nil {
		return d, err
	}
	tp, err := s.pvf.skillPointTables.TotalTechniquePoints(level)
	if err != nil {
		return d, err
	}
	return adminops.SkillPointDefinition{PVFChecksum: hash, Version: 1, Level: level, SP: sp, TP: tp}, nil
}

func (s *Server) v1SkillPoints(w http.ResponseWriter, r *http.Request) {
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
	u, ok := g.Character.(repository.SkillPointManagementReader)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	v, err := u.LoadSkillPointManagement(r.Context(), c.CharacterID)
	if err != nil {
		s.v1SkillPointResult(w, r, adminops.SkillPointReceipt{}, err)
		return
	}
	if v.Character.AccountID != c.AccountID || v.Character.Stats["delete_flag"] != 0 {
		s.v1SkillPointResult(w, r, adminops.SkillPointReceipt{}, adminops.ErrAccount)
		return
	}
	values, err := adminops.SkillPointValues(v)
	if err != nil {
		s.v1SkillPointResult(w, r, adminops.SkillPointReceipt{}, err)
		return
	}
	d, err := s.skillPointDefinition(r.Context(), v.Character.Level)
	if err != nil {
		s.v1SkillPointResult(w, r, adminops.SkillPointReceipt{}, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"characterId": c.CharacterID, "accountId": c.AccountID, "level": v.Character.Level, "balance": values, "definition": d, "definitionRevision": adminops.SkillPointDefinitionRevision(d), "revision": adminops.SkillPointRevision(v), "maximum": adminops.MaxSkillPoints}})
}

func (s *Server) v1SkillPointOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID          string `json:"accountId"`
		Action             string `json:"action"`
		Target             string `json:"target"`
		Amount             string `json:"amount"`
		Revision           string `json:"revision"`
		DefinitionRevision string `json:"definitionRevision"`
		Reason             string `json:"reason"`
		PlanToken          string `json:"planToken"`
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
	u, ok := g.Character.(repository.SkillPointManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:min(12, len(s.cfg.LoginKeySHA256))]
	}
	command := adminops.SkillPointCommand{AccountID: c.AccountID, CharacterID: c.CharacterID, Action: body.Action, Target: body.Target, Amount: body.Amount, Revision: body.Revision, DefinitionRevision: body.DefinitionRevision, Reason: body.Reason, Actor: actor, PlanToken: body.PlanToken, Key: key}
	if commit {
		old, found, e := adminops.LookupSkillPointOperation(r.Context(), u, command)
		if found || e != nil {
			s.v1SkillPointResult(w, r, old, e)
			return
		}
	}
	identity := command
	identity.PlanToken = ""
	fingerprint := adminops.SkillPointIdentity(identity)
	if commit {
		raw, e := s.v1CursorAfter(command.PlanToken, "skill-points-plan", fingerprint)
		at, parseErr := strconv.ParseInt(raw, 10, 64)
		age := time.Since(time.Unix(at, 0))
		if e != nil || parseErr != nil || age < 0 || age > 10*time.Minute {
			v1Fail(w, r, 409, "plan_conflict", "技能点预览已过期或内容已变更，请重新预览", false)
			return
		}
	} else {
		command.PlanToken = ""
	}
	result, err := adminops.RunSkillPointOperation(r.Context(), u, command, commit, s.skillPointDefinition, s.v1CharacterEditCheck(command.AccountID))
	if err == nil && !commit {
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result, "planToken": s.v1CursorToken("skill-points-plan", fingerprint, strconv.FormatInt(time.Now().Unix(), 10))}})
		return
	}
	s.v1SkillPointResult(w, r, result, err)
}

func (s *Server) v1SkillPointResult(w http.ResponseWriter, r *http.Request, result adminops.SkillPointReceipt, err error) {
	switch {
	case err == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(err, adminops.ErrConflict):
		v1Fail(w, r, 409, "revision_conflict", "角色或技能点已变更，请刷新后重新预览", false)
	case errors.Is(err, adminops.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(err, adminops.ErrSkillPointDefinition):
		v1Fail(w, r, 409, "pvf_conflict", "技能点PVF版本或等级基准已变更，请重新预览", false)
	case errors.Is(err, adminops.ErrCharacterOnline):
		v1Fail(w, r, 409, "character_online", "角色在线；到系统设置里打开“碳铀模式”来强制数据写入", false)
	case errors.Is(err, adminops.ErrRuntimeUnavailable):
		v1Fail(w, r, 503, "runtime_unavailable", "暂时无法取得账号编辑状态", true)
	case errors.Is(err, adminops.ErrSkillPointMissing):
		v1Fail(w, r, 422, "skill_ledger_missing", "该角色尚无技能点账本，请先完成角色初始化", false)
	case errors.Is(err, adminops.ErrSkillPointLedger):
		v1Fail(w, r, 422, "invalid_skill_ledger", "技能点存档超出范围或版本异常，请核对存档", false)
	case errors.Is(err, adminops.ErrSkillPointSpent):
		v1Fail(w, r, 422, "spent_points_conflict", "调整后的总量小于已消耗点数，请先处理技能分配", false)
	case errors.Is(err, adminops.ErrSkillPointInput):
		v1Fail(w, r, 422, "invalid_skill_points", "请检查操作、原因与点数，额外点数和总量须在0–65535之间", false)
	case errors.Is(err, adminops.ErrAccount) || errors.Is(err, sql.ErrNoRows):
		v1Fail(w, r, 404, "character_not_found", "角色不存在或归属已变更", false)
	default:
		s.v1Unavailable(w, r, err)
	}
}

func (s *Server) v1SkillPointLog(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "技能点记录查询参数无效", false)
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
	u, ok := g.Character.(repository.SkillPointManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID)
	raw, err := s.v1CursorAfter(q.Cursor, "skill-points-log", filter)
	var before int64
	if err == nil && raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "技能点记录分页条件已变更", false)
		return
	}
	rows, err := u.SkillPointManagementLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("skill-points-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.SkillPointReceipt{}
	for _, row := range rows {
		v, e := adminops.DecodeSkillPointReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, v)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
