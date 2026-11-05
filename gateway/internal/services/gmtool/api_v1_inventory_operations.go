package gmtool

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"dnf90.local/gateway/internal/diagnostics"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	mysqldriver "github.com/go-sql-driver/mysql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/inventory"
	"dnf90.local/gateway/internal/modules/dnf/itemquality"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1InventoryOptions(w http.ResponseWriter, r *http.Request) {
	spec, ok := repository.ResourceContainer(r.PathValue("container"))
	slot, e := strconv.Atoi(r.PathValue("slot"))
	if !ok || e != nil || slot < spec.First || slot > spec.Last || r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_slot", "物品位置无效", false)
		return
	}
	c, reader, ok := s.v1ResourceCharacter(w, r)
	if !ok || !s.v1RequireItemIndex(w, r) {
		return
	}
	rows, e := reader.InventoryPage(r.Context(), c, spec.ID, slot, 1, true)
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	row, present, found := v1SelectedInventoryRow(rows, spec.ID, slot)
	if !found {
		v1Fail(w, r, 404, "item_not_found", "该位置当前没有物品", false)
		return
	}
	v := adminops.InventorySelection{Container: spec.ID, Slot: slot}
	_, _, targetErr := adminops.InventoryTarget(v)
	item := s.v1InventoryRow(spec.ID, row, true)
	item["present"] = present
	item["revision"] = adminops.InventorySlotRevision(c.AccountID, c.CharacterID, spec.ID, slot, row.Stack, present)
	item["pvfChecksum"] = s.v1PVFChecksum()
	owner := spec.Owner
	if owner == "equipment" {
		owner = "character"
	}
	data := map[string]any{"item": item, "ownerScope": owner, "canDelete": targetErr == nil, "configuration": nil, "quantity": nil}
	if adminops.InventoryPetTarget(v) {
		linked, err := v1AttachPetState(r.Context(), reader, c, v, row.Stack, item)
		if err != nil {
			s.v1Unavailable(w, r, err)
			return
		}
		data["canDelete"] = targetErr == nil && linked
		v1Reply(w, r, 200, v1Envelope{Data: data})
		return
	}
	entry, e := s.itemcfgEntry(row.Stack.ItemID)
	data["canReplace"] = targetErr == nil && e == nil && !entry.Invalid && spec.ID != "shared" && !adminops.InventoryArtifactTarget(v) && entry.Tag != "" && (entry.Kind == "equipment" || entry.Kind == "stackable")
	if adminops.InventoryArtifactTarget(v) {
		linked, err := v1AttachArtifactState(r.Context(), reader, c, v, row.Stack, item)
		if err != nil {
			s.v1Unavailable(w, r, err)
			return
		}
		data["canDelete"] = targetErr == nil && linked
		if !linked {
			v1Reply(w, r, 200, v1Envelope{Data: data})
			return
		}
	}
	if targetErr == nil && e == nil && v1ArtifactDefinitionMatches(v, entry) {
		if spec.ID != "shared" {
			capability, err := s.itemcfgBuildCapability(entry, row.Stack, time.Now().UTC())
			if err == nil {
				data["configuration"] = capability
			}
		}
		if spec.ID != "equipped" && entry.Kind == "stackable" && v1InventoryCountLimit(entry, spec.ID) > 0 {
			minimum := "1"
			if spec.ID == "shared" {
				minimum = "0"
			}
			data["quantity"] = map[string]any{"min": minimum, "max": strconv.FormatInt(v1InventoryCountLimit(entry, spec.ID), 10), "current": strconv.FormatInt(row.Stack.Count, 10)}
		}
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}

func v1InventoryCountLimit(entry itemEntry, container string) int64 {
	// Shared crystal/soul balances are account counters, not ordinary bag stacks.
	// The current gameplay quantity writer uses a signed 32-bit representation.
	if container == "shared" {
		return 2147483647
	}
	return min(entry.StackLimit, 2147483647)
}

func (s *Server) v1InventoryProject(v adminops.InventorySelection, stack repository.ItemStack) adminops.InventoryState {
	row := s.v1InventoryRow(v.Container, repository.InventorySlot{Slot: v.Slot, Stack: stack}, true)
	return adminops.InventoryState{Present: true, ItemID: strconv.FormatInt(stack.ItemID, 10), Count: strconv.FormatInt(stack.Count, 10), Name: row["name"].(string), Bound: stack.Bind, ExpiresAt: stack.ExpireAt, Attributes: row["attributes"].(map[string]any)}
}

func inventoryPlanSeed(nonce string, v adminops.InventorySelection) uint32 {
	const acceptLimit = ^uint32(0) - (^uint32(0) % itemquality.RandomSeedCount)
	for i := 0; ; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d/%d", nonce, v.Container, v.Slot, i)))
		n := binary.LittleEndian.Uint32(sum[:4])
		if n < acceptLimit {
			return n%itemquality.RandomSeedCount + 1
		}
	}
}
func (s *Server) v1InventoryMutator(at time.Time, nonce string) adminops.InventoryMutator {
	return func(ctx context.Context, v adminops.InventorySelection, stack repository.ItemStack, options *adminops.InventoryOptions) (repository.ItemStack, error) {
		if e := ctx.Err(); e != nil {
			return repository.ItemStack{}, e
		}
		if len(stack.RawEntry) != 0 && len(stack.RawEntry) != itemcfgItemListEntrySize {
			return repository.ItemStack{}, errors.New("当前物品实例行格式尚未适配")
		}
		if len(stack.RawEntry) != 0 && int64(binary.LittleEndian.Uint32(stack.RawEntry[2:6])) != stack.ItemID {
			return repository.ItemStack{}, errors.New("物品实例行与物品编号不一致，请先核对实例数据")
		}
		spec, _, e := adminops.InventoryTarget(v)
		if e != nil {
			return repository.ItemStack{}, e
		}
		entry, e := s.itemcfgEntry(stack.ItemID)
		if e != nil {
			return repository.ItemStack{}, e
		}
		if entry.Invalid {
			return repository.ItemStack{}, errors.New("当前 PVF 将此物品标为无效")
		}
		if !v1ArtifactDefinitionMatches(v, entry) {
			return repository.ItemStack{}, errors.New("当前 PVF 的宠物装备类型与容器不匹配")
		}
		if options.ItemID != nil {
			return s.v1ReplaceInventoryItem(v, stack, entry, *options.ItemID, at)
		}
		if options.Count != nil {
			if v.Container == "equipped" {
				return repository.ItemStack{}, errors.New("穿戴实例的数量固定为一件")
			}
			count, e := strconv.ParseInt(*options.Count, 10, 64)
			if e != nil || strconv.FormatInt(count, 10) != *options.Count || entry.Kind != "stackable" {
				return repository.ItemStack{}, errors.New("仅堆叠物品支持整数数量修改")
			}
			if v.Container == "shared" {
				if count > 0 && len(stack.RawEntry) == 0 {
					definition, err := s.accountCubeDefinition(stack.ItemID)
					if err != nil {
						return repository.ItemStack{}, err
					}
					if definition.Kind != "stackable" || definition.PVFPath == "" || definition.StackableType == "" {
						return repository.ItemStack{}, errors.New("当前 PVF 的共享资源定义不完整")
					}
					if stack.Extra == nil {
						stack.Extra = map[string]string{}
					}
					stack.Extra["item_kind"], stack.Extra["pvf_path"], stack.Extra["stackable_type"] = definition.Kind, definition.PVFPath, definition.StackableType
					if definition.StackLimit > 0 {
						stack.Extra["stack_limit"] = strconv.FormatInt(definition.StackLimit, 10)
					}
				}
				stack, e = inventory.SetAdministrativeSharedCount(int16(v.Slot), stack, count)
			} else {
				stack, e = inventory.SetAdministrativeStackCount(stack, count, v1InventoryCountLimit(entry, v.Container))
			}
			if e != nil {
				return repository.ItemStack{}, errors.New("数量超出该容器的物品上限")
			}
		}
		legacy := &itemcfgOptionsRequest{QualityMode: options.QualityMode, UpgradeLevel: options.UpgradeLevel, AmplifyType: options.AmplifyType, ForgingLevel: options.ForgingLevel, AvatarOptionValue: options.AvatarOptionValue, ExpirationDays: options.ExpirationDays}
		if v.Container == "shared" && (legacy.QualityMode != nil || legacy.UpgradeLevel != nil || legacy.AmplifyType != nil || legacy.ForgingLevel != nil || legacy.AvatarOptionValue != nil || legacy.ExpirationDays != nil) {
			return repository.ItemStack{}, errors.New("账号晶块与灵魂仅支持修改数量")
		}
		if legacy.QualityMode == nil && legacy.UpgradeLevel == nil && legacy.AmplifyType == nil && legacy.ForgingLevel == nil && legacy.AvatarOptionValue == nil && legacy.ExpirationDays == nil {
			if options.Count == nil {
				return repository.ItemStack{}, errors.New("请选择需要修改的属性")
			}
			return itemcfgStampAuditKey(stack, at), nil
		}
		target, e := itemcfgResolveTarget(spec.List, v.Slot)
		if spec.ID == "account-cargo" || spec.ID == "equipped" {
			target, e = itemcfgTarget{ListType: spec.List, Slot: v.Slot, Key: fmt.Sprintf("%d:%d", spec.List, v.Slot)}, nil
		}
		if e != nil {
			return repository.ItemStack{}, e
		}
		seed := inventoryPlanSeed(nonce, v)
		applied, e := s.itemcfgApplyOptionsWithSeed(target, entry, stack, legacy, at, &seed)
		return applied.Stack, e
	}
}

func (s *Server) v1InventoryOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Force       bool                          `json:"force"`
		AccountID   string                        `json:"accountId"`
		Action      string                        `json:"action"`
		Items       []adminops.InventorySelection `json:"items"`
		Options     *adminops.InventoryOptions    `json:"options"`
		Reason      string                        `json:"reason"`
		PVFChecksum string                        `json:"pvfChecksum"`
		PlanToken   string                        `json:"planToken"`
	}
	if !v1DecodeLimit(w, r, &body, 64<<10) {
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
		v1Fail(w, r, 409, "owner_conflict", "角色所属账号已变更", false)
		return
	}
	g, e := s.repositories()
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	scope, e := adminops.InventoryScope(body.Items)
	if e != nil {
		s.v1InventoryOperationResult(w, r, adminops.InventoryReceipt{}, e)
		return
	}
	u, ok := v1InventoryUnitOfWork(g, scope)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:12]
	}
	command := adminops.InventoryCommand{Force: body.Force, AccountID: c.AccountID, CharacterID: c.CharacterID, Action: body.Action, Items: body.Items, Options: body.Options, Reason: body.Reason, PVFChecksum: body.PVFChecksum, Actor: actor, PlanToken: body.PlanToken, Key: key}
	if commit {
		done := diagnostics.Start(r.Context(), "inventory.receipt_lookup")
		old, found, e := adminops.LookupInventoryOperation(r.Context(), u, command)
		done(e)
		if found || e != nil {
			s.v1InventoryOperationResult(w, r, old, e)
			return
		}
	}
	if !s.v1RequireItemIndex(w, r) {
		return
	}
	if command.PVFChecksum != s.v1PVFChecksum() {
		v1Fail(w, r, 409, "pvf_conflict", "PVF 已变更，请重新读取物品并预览", false)
		return
	}
	identity := command
	identity.PlanToken = ""
	fingerprint := adminops.InventoryIdentity(identity)
	at := time.Now().UTC().Truncate(time.Second)
	nonce := ""
	if commit {
		raw, e := s.v1CursorAfter(command.PlanToken, "inventory-plan", fingerprint)
		parts := strings.Split(raw, ":")
		if e != nil || len(parts) != 2 {
			v1Fail(w, r, 409, "plan_conflict", "物品预览与提交内容不一致，请重新预览", false)
			return
		}
		unix, e := strconv.ParseInt(parts[0], 10, 64)
		at = time.Unix(unix, 0).UTC()
		decoded, ne := hex.DecodeString(parts[1])
		nonce = parts[1]
		if e != nil || ne != nil || len(decoded) != 16 || time.Since(at) < 0 || time.Since(at) > 10*time.Minute {
			v1Fail(w, r, 409, "plan_expired", "预览已过期，请重新预览", false)
			return
		}
	} else {
		var random [16]byte
		if _, e = rand.Read(random[:]); e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		nonce = hex.EncodeToString(random[:])
		command.PlanToken = s.v1CursorToken("inventory-plan", fingerprint, fmt.Sprintf("%d:%s", at.Unix(), nonce))
	}
	check := s.v1CharacterEditCheck(command.AccountID)
	if scope, err := adminops.InventoryScope(command.Items); err == nil && scope == "account" {
		check = s.v1AccountEditCheck(command.AccountID)
	}
	forceAvailable := false
	if command.Force && !carbonMode(r.Context()) {
		forceAvailable = s.inventoryForceSupported(r.Context())
	}
	if command.Force && !carbonMode(r.Context()) && !forceAvailable {
		s.v1InventoryOperationResult(w, r, adminops.InventoryReceipt{}, errInventoryForceUnavailable)
		return
	}
	if commit {
		if command.Force && !carbonMode(r.Context()) {
			var release func()
			done := diagnostics.Start(r.Context(), "inventory.drain")
			check, release, e = s.acquireInventoryEdit(r.Context(), command.AccountID)
			done(e)
			if e != nil {
				s.v1InventoryOperationResult(w, r, adminops.InventoryReceipt{}, e)
				return
			}
			defer release()
		}
		// Reject an occupied owner before taking SQL asset locks. Checks inside
		// the transaction remain authoritative against a concurrent login.
		if e = check(r.Context(), command.CharacterID); e != nil {
			s.v1InventoryOperationResult(w, r, adminops.InventoryReceipt{}, e)
			return
		}
	}
	done := diagnostics.Start(r.Context(), "inventory.transaction")
	result, e := adminops.RunInventoryOperation(r.Context(), u, command, commit, s.v1InventoryMutator(at, nonce), s.v1InventoryProject, check)
	done(e)
	if e == nil && !commit {
		v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"receipt": result, "planToken": command.PlanToken, "forceAvailable": forceAvailable}})
		return
	}
	s.v1InventoryOperationResult(w, r, result, e)
}

func (s *Server) v1InventoryOperationResult(w http.ResponseWriter, r *http.Request, result adminops.InventoryReceipt, e error) {
	if e == nil {
		v1Reply(w, r, 200, v1Envelope{Data: result})
		return
	}
	status, code, message, retry := 422, "inventory_rejected", "物品操作条件不满足", false
	var databaseError *mysqldriver.MySQLError
	switch {
	case errors.Is(e, context.DeadlineExceeded):
		if s.cfg.Logger != nil {
			diagnostics.Log(s.cfg.Logger, r.Context(), "gm-inventory-write-timeout", e)
		}
		status, code, message, retry = 503, "inventory_timeout", "物品修改等待超时，可使用本次请求继续查询结果", true
	case errors.Is(e, errInventoryForceUnavailable):
		status, code, message = 409, "force_unavailable", "游戏服务尚未支持强制编辑，需更新游戏接口"
	case errors.Is(e, errInventoryLeaseUnavailable):
		status, code, message, retry = 503, "inventory_lease_unavailable", "暂时无法取得强制编辑状态，请稍后继续本次操作", true
	case errors.Is(e, errInventoryDraining):
		status, code, message, retry = 503, "inventory_draining", "正在等待原连接完成存档，请稍后继续本次操作", true
	case errors.As(e, &databaseError) && (databaseError.Number == 1213 || databaseError.Number == 1205):
		status, code, message, retry = 503, "inventory_busy", "库存正在被其他事务使用，请稍后继续本次操作", true
	case errors.Is(e, adminops.ErrConflict):
		status, code, message = 409, "revision_conflict", "物品实例已变更，请重新读取并预览"
	case errors.Is(e, adminops.ErrIdempotency):
		status, code, message = 409, "idempotency_conflict", "此请求编号已用于其他操作"
	case errors.Is(e, adminops.ErrCharacterOnline):
		status, code, message = 409, "character_online", "角色在线；到系统设置里打开“碳铀模式”来强制数据写入"
	case errors.Is(e, adminops.ErrRuntimeUnavailable):
		status, code, message, retry = 503, "runtime_unavailable", "暂时无法取得账号编辑状态", true
	case errors.Is(e, adminops.ErrAccount) || errors.Is(e, sql.ErrNoRows):
		status, code, message = 404, "character_not_found", "角色不存在或归属已变更"
	case errors.Is(e, adminops.ErrInventoryInput):
		status, code, message = 400, "invalid_operation", "请检查物品操作、位置、版本与原因"
	case errors.Is(e, adminops.ErrInventoryContainer):
		message = "该容器需要专用管理操作"
	case errors.Is(e, adminops.ErrInventorySlot):
		status, code, message = 409, "slot_conflict", "所选位置已没有原物品，请刷新"
	default:
		var position *adminops.InventoryOperationError
		if !errors.As(e, &position) {
			s.v1Unavailable(w, r, e)
			return
		}
		message = itemcfgReadErrorMessage(position.Err)
	}
	var position *adminops.InventoryOperationError
	if errors.As(e, &position) {
		if spec, ok := repository.ResourceContainer(position.Container); ok {
			message = fmt.Sprintf("%s 槽位 %d：%s", spec.Name, position.Slot, message)
		}
	}
	v1Fail(w, r, status, code, message, retry)
}

func v1InventoryUnitOfWork(g repository.Group, scope string) (repository.InventoryManagementUnitOfWork, bool) {
	if scope == "account" {
		u, ok := g.AccountInventory.(repository.InventoryManagementUnitOfWork)
		return u, ok
	}
	u, ok := g.Character.(repository.InventoryManagementUnitOfWork)
	return u, ok
}

func (s *Server) v1InventoryOperationLog(w http.ResponseWriter, r *http.Request) {
	q, e := v1ParseQuery(r)
	if e != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "物品流水查询参数无效", false)
		return
	}
	var g repository.Group
	var id, binding, scope string
	if r.PathValue("accountId") != "" {
		var ok bool
		g, id, ok = s.v1CurrencyAccount(w, r)
		if !ok {
			return
		}
		scope, binding = "account", id
	} else {
		c, _, ok := s.v1ResourceCharacter(w, r)
		if !ok {
			return
		}
		g, e = s.repositories()
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		id, binding, scope = c.CharacterID, c.CharacterID+"\x00"+c.AccountID, "character"
	}
	u, ok := v1InventoryUnitOfWork(g, scope)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, binding)
	kind := scope + "-inventory-operations"
	raw, e := s.v1CursorAfter(q.Cursor, kind, filter)
	var before int64
	if e == nil && raw != "" {
		before, e = strconv.ParseInt(raw, 10, 64)
	}
	if e != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "物品流水分页条件已变更", false)
		return
	}
	rows, e := u.InventoryManagementLog(r.Context(), id, before, q.Limit+1)
	if e != nil {
		s.v1Unavailable(w, r, e)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken(kind, filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	items := []adminops.InventoryReceipt{}
	for _, row := range rows {
		receipt, e := adminops.DecodeInventoryReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		items = append(items, receipt)
	}
	v1Reply(w, r, 200, v1Envelope{Data: items, Page: &page})
}
