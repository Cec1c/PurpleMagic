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

func (s *Server) vaultDefinition() (adminops.VaultDefinition, error) {
	if s.deps.Catalog != nil {
		var d adminops.VaultDefinition
		e := s.deps.Catalog.background(catalogRequest{Operation: "vault"}, &d)
		return d, e
	}
	if s.deps.PVF == nil {
		return adminops.VaultDefinition{}, adminops.ErrVaultDefinition
	}
	source, e := s.source()
	if e != nil || source == nil {
		return adminops.VaultDefinition{}, adminops.ErrVaultDefinition
	}
	text, e := source.ReadText(adminops.VaultDefinitionPath)
	if e != nil {
		return adminops.VaultDefinition{}, adminops.ErrVaultDefinition
	}
	return adminops.ParseVaultDefinition(text, itemIndexSourceChecksum(source))
}

func (s *Server) v1Vault(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	reader, ok := g.Account.(repository.AccountVaultReader)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	snapshot, e := reader.LoadAccountVault(r.Context(), id)
	if e != nil {
		s.v1VaultResult(w, r, adminops.VaultReceipt{}, e)
		return
	}
	values, e := adminops.ReadVaultValues(snapshot)
	if e != nil {
		s.v1VaultResult(w, r, adminops.VaultReceipt{}, e)
		return
	}
	definition, e := s.vaultDefinition()
	if e != nil {
		s.v1VaultResult(w, r, adminops.VaultReceipt{}, e)
		return
	}
	limit, _ := definition.GoldLimit(values.Capacity)
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"accountId": id, "values": values, "definition": definition, "maximumGold": strconv.FormatInt(limit, 10), "revision": adminops.VaultRevision(snapshot)}})
}
func (s *Server) v1VaultOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DefinitionRevision string                    `json:"definitionRevision"`
		Gold               *adminops.VaultGoldChange `json:"gold"`
		Capacity           *int                      `json:"capacity"`
		Revision           string                    `json:"revision"`
		Reason             string                    `json:"reason"`
		PlanToken          string                    `json:"planToken"`
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
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AccountVaultUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:12]
	}
	command := adminops.VaultCommand{DefinitionRevision: body.DefinitionRevision, AccountID: id, Gold: body.Gold, Capacity: body.Capacity, Revision: body.Revision, Reason: body.Reason, Actor: actor, PlanToken: body.PlanToken, Key: key}
	if commit {
		old, found, e := adminops.LookupVaultOperation(r.Context(), u, command)
		if found || e != nil {
			s.v1VaultResult(w, r, old, e)
			return
		}
	}
	identity := command
	identity.PlanToken = ""
	fingerprint := adminops.VaultIdentity(identity)
	if commit {
		raw, e := s.v1CursorAfter(command.PlanToken, "vault-plan", fingerprint)
		at, parseErr := strconv.ParseInt(raw, 10, 64)
		age := time.Since(time.Unix(at, 0))
		if e != nil || parseErr != nil || age < 0 || age > 10*time.Minute {
			v1Fail(w, r, 409, "plan_conflict", "金库预览已过期或内容已变更，请重新预览", false)
			return
		}
	} else {
		command.PlanToken = ""
	}
	definition, e := s.vaultDefinition()
	if e != nil {
		s.v1VaultResult(w, r, adminops.VaultReceipt{}, e)
		return
	}
	result, e := adminops.RunVaultOperation(r.Context(), u, command, definition, commit, s.v1AccountEditCheck(command.AccountID))
	if e == nil && !commit {
		token := s.v1CursorToken("vault-plan", fingerprint, strconv.FormatInt(time.Now().Unix(), 10))
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result, "planToken": token}})
		return
	}
	s.v1VaultResult(w, r, result, e)
}
func (s *Server) v1VaultResult(w http.ResponseWriter, r *http.Request, result adminops.VaultReceipt, e error) {
	switch {
	case e == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(e, adminops.ErrConflict):
		v1Fail(w, r, 409, "revision_conflict", "账号金库已变更，请刷新后重新预览", false)
	case errors.Is(e, adminops.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(e, adminops.ErrCharacterOnline):
		v1Fail(w, r, 409, "character_online", "角色在线；到系统设置里打开“碳铀模式”来强制数据写入", false)
	case errors.Is(e, adminops.ErrRuntimeUnavailable):
		v1Fail(w, r, 503, "runtime_unavailable", "暂时无法取得账号编辑状态", true)
	case errors.Is(e, adminops.ErrVaultDefinitionChanged):
		v1Fail(w, r, 409, "vault_definition_changed", "PVF金库配置已更新，请刷新后重新预览", false)
	case errors.Is(e, adminops.ErrVaultDefinition):
		v1Fail(w, r, 503, "vault_definition_unavailable", "当前PVF的金库配置暂不可用", true)
	case errors.Is(e, adminops.ErrVaultInput):
		v1Fail(w, r, 422, "invalid_vault_operation", "请检查金库容量、金币数量和操作原因", false)
	case errors.Is(e, adminops.ErrVaultNotCreated):
		v1Fail(w, r, 409, "vault_not_created", "请先选择容量开通账号金库", false)
	case errors.Is(e, adminops.ErrVaultDecrease):
		v1Fail(w, r, 422, "capacity_decrease", "金库扩容不能降低当前容量", false)
	case errors.Is(e, repository.ErrVaultState):
		v1Fail(w, r, 422, "invalid_vault_state", "账号金库存档无效，请核对容量、金币和开通状态", false)
	case errors.Is(e, adminops.ErrAccount) || errors.Is(e, sql.ErrNoRows):
		v1Fail(w, r, 404, "account_not_found", "账号存档不存在", false)
	default:
		s.v1Unavailable(w, r, e)
	}
}
func (s *Server) v1VaultLog(w http.ResponseWriter, r *http.Request) {
	q, e := v1ParseQuery(r)
	if e != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "金库流水查询参数无效", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AccountVaultUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, id)
	raw, e := s.v1CursorAfter(q.Cursor, "vault-log", filter)
	var before int64
	if e == nil && raw != "" {
		before, e = strconv.ParseInt(raw, 10, 64)
	}
	if e != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "金库流水分页条件已变更", false)
		return
	}
	rows, e := u.AccountVaultLog(r.Context(), id, before, q.Limit+1)
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("vault-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.VaultReceipt{}
	for _, row := range rows {
		item, e := adminops.DecodeVaultReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, item)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
