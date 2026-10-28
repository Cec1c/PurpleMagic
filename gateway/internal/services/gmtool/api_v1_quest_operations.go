package gmtool

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) questOperationCatalog(ctx context.Context, ix *questDiskIndex, ids []int64) (*quest.Catalog, error) {
	queue := append([]int64(nil), ids...)
	loaded := map[int64]bool{}
	definitions := []quest.Definition{}
	for len(queue) > 0 {
		if ix.remote != nil {
			ids := []int64{}
			for _, id := range queue {
				if _, ok := ix.remoteRows[id]; !ok {
					ids = append(ids, id)
				}
				if len(ids) == 200 {
					break
				}
			}
			if len(ids) > 0 {
				if e := ix.remoteLoad(ctx, ids); e != nil {
					return nil, e
				}
			}
		}
		id := queue[0]
		queue = queue[1:]
		if loaded[id] {
			continue
		}
		loaded[id] = true
		if len(loaded) > 4096 {
			return nil, errors.New("quest operation dependency limit exceeded")
		}
		d, err := ix.definition(ctx, id)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, d)
		if d.MainQuestID > 0 {
			queue = append(queue, d.MainQuestID)
		}
		if normalizeQuestRewardTag(d.Type) == "quest clear" {
			queue = append(queue, d.IntData...)
		}
	}
	return quest.CatalogFromDefinitions(definitions)
}

// Used inside the account-row transaction, before mutations and before commit.
func (s *Server) v1AccountEditCheck(account string) adminops.QuestOfflineCheck {
	return func(ctx context.Context, _ string) error {
		if s.deps.Runtime == nil || account == "" {
			return adminops.ErrRuntimeUnavailable
		}
		result, err := s.deps.Runtime.Call(ctx, adminapi.Request{Action: "account_edit_status", AccountID: account})
		if err != nil || !result.OK || result.AccountEdit == nil || result.AccountEdit.Protocol != adminapi.AccountEditProtocol || result.AccountEdit.AccountID != account {
			return adminops.ErrRuntimeUnavailable
		}
		if result.AccountEdit.Busy {
			return adminops.ErrCharacterOnline
		}
		return nil
	}
}

func (s *Server) v1QuestOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action      string   `json:"action"`
		QuestIDs    []string `json:"questIds"`
		AccountID   string   `json:"accountId"`
		Reason      string   `json:"reason"`
		Revision    string   `json:"revision"`
		PVFChecksum string   `json:"pvfChecksum"`
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
	if len(body.QuestIDs) < 1 || len(body.QuestIDs) > 200 {
		v1Fail(w, r, 400, "invalid_quests", "请选择 1 至 200 条任务", false)
		return
	}
	ids := make([]int64, 0, len(body.QuestIDs))
	seen := map[int64]bool{}
	for _, raw := range body.QuestIDs {
		id, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || id <= 0 || id > 65535 || strconv.FormatInt(id, 10) != raw || seen[id] {
			v1Fail(w, r, 400, "invalid_quests", "任务编号无效或重复", false)
			return
		}
		ids = append(ids, id)
		seen[id] = true
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
	u, ok := g.Character.(repository.CharacterManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:12]
	}
	command := adminops.QuestCommand{AccountID: c.AccountID, CharacterID: c.CharacterID, Action: body.Action, QuestIDs: ids, Reason: body.Reason, Revision: body.Revision, PVFChecksum: body.PVFChecksum, Actor: actor, Key: key}
	if commit {
		done := diagnostics.Start(r.Context(), "quest.receipt_lookup")
		result, found, err := adminops.LookupQuestOperation(r.Context(), u, command)
		done(err)
		if err != nil || found {
			s.v1QuestOperationResult(w, r, result, err)
			return
		}
	}
	ix, ok := s.v1QuestIndex(w, r)
	if !ok {
		return
	}
	if commit && body.PVFChecksum != ix.Checksum {
		v1Fail(w, r, 409, "pvf_conflict", "PVF 已变更，请重新预览任务操作", false)
		return
	}
	done := diagnostics.Start(r.Context(), "quest.definitions")
	catalog, err := s.questOperationCatalog(r.Context(), ix, ids)
	done(err)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			v1Fail(w, r, 422, "definition_missing", "当前 PVF 缺少任务或关联任务定义", false)
		} else {
			s.v1Unavailable(w, r, err)
		}
		return
	}
	command.PVFChecksum = ix.Checksum
	done = diagnostics.Start(r.Context(), "quest.operation")
	result, err := adminops.RunQuestOperation(r.Context(), u, catalog, command, commit, s.v1CharacterEditCheck(command.AccountID))
	done(err)
	s.v1QuestOperationResult(w, r, result, err)
}

func (s *Server) v1QuestOperationResult(w http.ResponseWriter, r *http.Request, result adminops.QuestReceipt, err error) {
	fail := func(status int, code, message string, retry bool) {
		var operation *quest.AdministrationError
		if errors.As(err, &operation) {
			message = "任务 " + strconv.FormatInt(operation.QuestID, 10) + "：" + message
		}
		v1Fail(w, r, status, code, message, retry)
	}
	switch {
	case err == nil:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	case errors.Is(err, context.DeadlineExceeded):
		diagnostics.Fail(r.Context(), err)
		fail(503, "quest_operation_timeout", "任务操作超时，请继续查询原请求回执", true)
	case errors.Is(err, adminops.ErrConflict):
		fail(409, "revision_conflict", "角色或任务已变更，请重新预览", false)
	case errors.Is(err, adminops.ErrIdempotency):
		fail(409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(err, adminops.ErrCharacterOnline):
		fail(409, "character_online", "目标角色仍在线或正在退出，请返回角色选择界面后操作", false)
	case errors.Is(err, adminops.ErrRuntimeUnavailable):
		fail(503, "runtime_unavailable", "暂时无法取得账号编辑状态", true)
	case errors.Is(err, adminops.ErrAccount) || errors.Is(err, sql.ErrNoRows):
		fail(404, "character_not_found", "角色不存在或归属已变更", false)
	case errors.Is(err, adminops.ErrQuestInput):
		fail(400, "invalid_operation", "请检查任务操作、原因和角色标识", false)
	case errors.Is(err, quest.ErrAdministrationProfession):
		fail(422, "profession_operation_required", "职业任务需通过职业变更操作完成", false)
	case errors.Is(err, quest.ErrAdministrationSettlement):
		fail(422, "settlement_exists", "该任务已有结算记录，请先核对任务详情", false)
	case errors.Is(err, quest.ErrQuestRewardMalformed):
		fail(422, "quest_reward_malformed", "当前 PVF 的开槽奖励定义无效", false)
	case errors.Is(err, quest.ErrQuestNotAcceptable):
		fail(422, "not_acceptable", "当前角色不满足任务的等级、职业或前置条件", false)
	case errors.Is(err, quest.ErrQuestCannotGiveUp):
		fail(422, "cannot_give_up", "PVF 标记该任务不可放弃", false)
	case errors.Is(err, quest.ErrGiveUpNeedsAssets) || errors.Is(err, quest.ErrQuestAcceptEventItemsRequired):
		fail(422, "quest_assets_required", "该任务需要同时处理任务物品，请通过游戏任务流程操作", false)
	case errors.Is(err, quest.ErrQuestNotActive):
		fail(422, "quest_state_conflict", "当前任务状态不适用此操作", false)
	case errors.Is(err, quest.ErrQuestDefinitionMissing):
		fail(422, "definition_missing", "当前 PVF 缺少任务定义", false)
	case errors.Is(err, quest.ErrQuestChainChildUnresolved) || errors.Is(err, quest.ErrQuestChainParentMissing):
		fail(422, "chain_incomplete", "关联任务定义不完整", false)
	case errors.Is(err, quest.ErrQuestChainIncompleteReactivation) || errors.Is(err, quest.ErrQuestChainParentNotQuestClear) || errors.Is(err, quest.ErrQuestChainRequestedNotManaged) || errors.Is(err, quest.ErrQuestChainNotLinked) || errors.Is(err, quest.ErrQuestChainRowMissing):
		fail(422, "chain_reset_rejected", "当前角色或链路状态无法重置该任务家族", false)
	default:
		s.v1Unavailable(w, r, err)
	}
}

func (s *Server) v1QuestOperationLog(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "任务流水查询参数无效", false)
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
	u, ok := g.Character.(repository.CharacterManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, c.CharacterID+"\x00"+c.AccountID)
	raw, err := s.v1CursorAfter(q.Cursor, "quest-operations", filter)
	var before int64
	if err == nil && raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "任务流水分页条件已变更", false)
		return
	}
	rows, err := u.CharacterManagementLog(r.Context(), c.CharacterID, before, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("quest-operations", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.QuestReceipt{}
	for _, row := range rows {
		receipt, e := adminops.DecodeQuestReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, receipt)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
