package gmtool

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func cloneDungeonQuotaCatalog(d adminops.DungeonQuotaCatalog) adminops.DungeonQuotaCatalog {
	d.EchonTempleIDs = slices.Clone(d.EchonTempleIDs)
	d.EchonTrialIDs = slices.Clone(d.EchonTrialIDs)
	return d
}

func (s *Server) dungeonQuotaNow() time.Time {
	if s.quotaNow != nil {
		return s.quotaNow()
	}
	return time.Now().UTC()
}

func (s *Server) dungeonQuotaCatalog(ctx context.Context) (adminops.DungeonQuotaCatalog, error) {
	if s.deps.Catalog != nil {
		return s.deps.Catalog.dungeonQuotaCatalog(ctx)
	}
	var d adminops.DungeonQuotaCatalog
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
	if s.pvf.dungeonQuotaChecksum != hash || s.pvf.dungeonQuotaCatalog == nil {
		loaded, e := adminops.LoadDungeonQuotaCatalog(source, hash)
		if e != nil {
			return d, e
		}
		cached := cloneDungeonQuotaCatalog(loaded)
		s.pvf.dungeonQuotaCatalog, s.pvf.dungeonQuotaChecksum = &cached, hash
	}
	return cloneDungeonQuotaCatalog(*s.pvf.dungeonQuotaCatalog), nil
}

func (s *Server) v1DungeonQuotas(w http.ResponseWriter, r *http.Request) {
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
	u, ok := g.Character.(repository.DungeonQuotaManagementReader)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	policy, err := adminops.DefaultDungeonQuotaWeeklyPolicy()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	d, err := s.dungeonQuotaCatalog(r.Context())
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	v, err := u.LoadDungeonQuotaManagement(r.Context(), c.CharacterID)
	if err != nil {
		s.v1DungeonQuotaResult(w, r, adminops.DungeonQuotaReceipt{}, err)
		return
	}
	if v.Character.AccountID != c.AccountID || v.Character.Stats["delete_flag"] != 0 {
		s.v1DungeonQuotaResult(w, r, adminops.DungeonQuotaReceipt{}, adminops.ErrAccount)
		return
	}
	now := s.dungeonQuotaNow()
	quotas, err := adminops.DungeonQuotaValues(v, d, now, policy)
	if err != nil {
		s.v1DungeonQuotaResult(w, r, adminops.DungeonQuotaReceipt{}, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{
		"accountId":          c.AccountID,
		"characterId":        c.CharacterID,
		"characterLevel":     v.Character.Level,
		"quotas":             quotas,
		"pvfChecksum":        d.PVFChecksum,
		"definitionRevision": adminops.DungeonQuotaDefinitionRevision(d),
		"revision":           adminops.DungeonQuotaRevision(v, d, now, policy),
	}})
}

func (s *Server) v1DungeonQuotaOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID          string `json:"accountId"`
		Action             string `json:"action"`
		QuotaKey           string `json:"quotaKey"`
		Revision           string `json:"revision"`
		PVFChecksum        string `json:"pvfChecksum"`
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
	u, ok := g.Character.(repository.DungeonQuotaManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	policy, err := adminops.DefaultDungeonQuotaWeeklyPolicy()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:min(12, len(s.cfg.LoginKeySHA256))]
	}
	command := adminops.DungeonQuotaCommand{
		AccountID:          c.AccountID,
		CharacterID:        c.CharacterID,
		Action:             body.Action,
		QuotaKey:           body.QuotaKey,
		Revision:           body.Revision,
		PVFChecksum:        body.PVFChecksum,
		DefinitionRevision: body.DefinitionRevision,
		Reason:             body.Reason,
		Actor:              actor,
		PlanToken:          body.PlanToken,
		Key:                key,
	}
	if commit {
		old, found, e := adminops.LookupDungeonQuotaOperation(r.Context(), u, command)
		if found || e != nil {
			s.v1DungeonQuotaResult(w, r, old, e)
			return
		}
		identity := command
		identity.PlanToken = ""
		raw, e := s.v1CursorAfter(command.PlanToken, "dungeon-quotas-plan", adminops.DungeonQuotaIdentity(identity))
		at, parseErr := strconv.ParseInt(raw, 10, 64)
		age := time.Since(time.Unix(at, 0))
		if e != nil || parseErr != nil || age < 0 || age > 10*time.Minute {
			v1Fail(w, r, 409, "plan_conflict", "副本额度预览已过期或内容已变更，请重新预览", false)
			return
		}
	} else {
		command.PlanToken = ""
	}
	result, err := adminops.RunDungeonQuotaOperation(r.Context(), u, command, commit, s.dungeonQuotaNow(), policy, s.dungeonQuotaCatalog, s.v1CharacterEditCheck(command.AccountID))
	if err == nil && !commit {
		command.DefinitionRevision = result.DefinitionRevision
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{
			"receipt":            result,
			"definitionRevision": result.DefinitionRevision,
			"planToken":          s.v1CursorToken("dungeon-quotas-plan", adminops.DungeonQuotaIdentity(command), strconv.FormatInt(time.Now().Unix(), 10)),
		}})
		return
	}
	s.v1DungeonQuotaResult(w, r, result, err)
}

func (s *Server) v1DungeonQuotaResult(w http.ResponseWriter, r *http.Request, result adminops.DungeonQuotaReceipt, err error) {
	switch {
	case err == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(err, adminops.ErrDungeonQuotaZero):
		v1Fail(w, r, 422, "quota_not_used", "当前副本未消耗次数，无需重置", false)
	case errors.Is(err, adminops.ErrDungeonQuotaDefinition):
		v1Fail(w, r, 409, "pvf_conflict", "副本额度定义已变更，请刷新后重新预览", false)
	case errors.Is(err, adminops.ErrDungeonQuotaInput):
		v1Fail(w, r, 422, "invalid_dungeon_quota", "请选择有效的副本额度重置项", false)
	case errors.Is(err, adminops.ErrDungeonQuotaState):
		v1Fail(w, r, 422, "invalid_dungeon_quota_state", "角色副本计数存档异常，请核对存档", false)
	case errors.Is(err, adminops.ErrConflict):
		v1Fail(w, r, 409, "revision_conflict", "角色副本额度已变更，请刷新后重新预览", false)
	case errors.Is(err, adminops.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(err, adminops.ErrCharacterOnline):
		v1Fail(w, r, 409, "character_online", "角色在线；到系统设置里打开“碳铀模式”来强制数据写入", false)
	case errors.Is(err, adminops.ErrRuntimeUnavailable):
		v1Fail(w, r, 503, "runtime_unavailable", "暂时无法取得账号编辑状态", true)
	case errors.Is(err, adminops.ErrAccount) || errors.Is(err, sql.ErrNoRows):
		v1Fail(w, r, 404, "character_not_found", "角色不存在或归属已变更", false)
	default:
		s.v1Unavailable(w, r, err)
	}
}

func (s *Server) v1DungeonQuotaLog(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "副本额度记录查询参数无效", false)
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
	u, ok := g.Character.(repository.DungeonQuotaManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID)
	raw, err := s.v1CursorAfter(q.Cursor, "dungeon-quotas-log", filter)
	var before int64
	if err == nil && raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "副本额度记录分页条件已变更", false)
		return
	}
	rows, err := u.DungeonQuotaManagementLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("dungeon-quotas-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.DungeonQuotaReceipt{}
	for _, row := range rows {
		v, e := adminops.DecodeDungeonQuotaReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, v)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
