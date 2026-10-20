package gmtool

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
	"dnf90.local/gateway/internal/servicehttp"
)

func (s *Server) v1OperationRoutes(read, write *http.ServeMux) {
	read.HandleFunc("GET /api/v1/benefits/{id}/pvf-history", s.v1CampaignPVFHistory)
	write.HandleFunc("POST /api/v1/benefits/{id}/pvf-preview", s.v1CampaignPVFPreview)
	write.HandleFunc("POST /api/v1/benefits/{id}/pvf-apply", s.v1CampaignPVFApply)
	read.HandleFunc("GET /api/v1/items/{itemId}/grant-options", s.v1MailGrantOptions)
	read.HandleFunc("GET /api/v1/benefits", s.v1Campaigns)
	read.HandleFunc("GET /api/v1/benefits/{id}", s.v1Campaign)
	read.HandleFunc("GET /api/v1/tasks", s.v1Tasks)
	read.HandleFunc("GET /api/v1/tasks/{id}", s.v1Task)
	read.HandleFunc("GET /api/v1/tasks/{id}/confirmation", s.v1Task)
	read.HandleFunc("GET /api/v1/tasks/{id}/recipients", s.v1Recipients)
	write.HandleFunc("POST /api/v1/benefits", s.v1CreateCampaign)
	write.HandleFunc("POST /api/v1/benefits/{id}/state", s.v1CampaignState)
	write.HandleFunc("POST /api/v1/benefits/{id}/preview", s.v1CampaignPreview)
	write.HandleFunc("POST /api/v1/mail/preview", s.v1MailPreview)
	write.HandleFunc("POST /api/v1/mail/online-preview", s.v1OnlineMailPreview)
	write.HandleFunc("POST /api/v1/tasks/{id}/run", s.v1RunTask)
}
func (s *Server) v1CSRF(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, []byte("gm-v1-csrf:"+s.cfg.Password))
	mac.Write([]byte(c.Value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Server) v1CheckWrite(w http.ResponseWriter, r *http.Request) bool {
	if !servicehttp.Authenticated(r) && (r.Header.Get("Sec-Fetch-Site") == "cross-site" || s.v1CSRF(r) == "" || !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.v1CSRF(r)))) {
		v1Fail(w, r, 403, "csrf_required", "请刷新页面后重试", false)
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		v1Fail(w, r, 415, "json_required", "请求须为 JSON", false)
		return false
	}
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "写入接口不接受查询参数", false)
		return false
	}
	return true
}
func v1Decode(w http.ResponseWriter, r *http.Request, out any) bool {
	return v1DecodeLimit(w, r, out, 4096)
}

// Batch routes opt into a bounded larger body for per-item revision hashes.
func v1DecodeLimit(w http.ResponseWriter, r *http.Request, out any, limit int64) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if dec.Decode(out) == nil && dec.Decode(new(any)) == io.EOF {
			return true
		}
	}
	v1Fail(w, r, 400, "invalid_body", "请求内容无效，请检查输入", false)
	return false
}
func v1Idempotency(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	valid := len(key) >= 16 && len(key) <= 128
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			valid = false
		}
	}
	if !valid {
		v1Fail(w, r, 400, "idempotency_key_required", "缺少有效的请求编号", false)
	}
	return key, valid
}
func (s *Server) v1OperationReady(w http.ResponseWriter, r *http.Request) bool {
	if id := r.PathValue("id"); id != "" {
		if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 16 {
			v1Fail(w, r, 404, "not_found", "记录不存在", false)
			return false
		}
	}
	if s.deps.Benefits == nil {
		s.v1Unavailable(w, r, errors.New("benefit store unavailable"))
		return false
	}
	return true
}

func (s *Server) v1OperationGroup() (repository.Group, error) {
	if s.deps.Repos == nil {
		return repository.Group{}, errRepositoriesUnavailable
	}
	g, err := s.repositories()
	if err != nil {
		return g, err
	}
	if g.Account == nil {
		return g, errRepositoriesUnavailable
	}
	if _, ok := g.MailboxAssets.(repository.AccountMailboxCharacterUnitOfWork); !ok {
		return g, repository.ErrMailboxAssetTransactionUnavailable
	}
	return g, nil
}
func (s *Server) v1OperationError(w http.ResponseWriter, r *http.Request, err error) {
	var input *v1MailInputError
	if errors.As(err, &input) {
		v1Fail(w, r, input.status, input.code, input.message, false)
		return
	}
	switch {
	case errors.Is(err, testbenefits.ErrPVFReview):
		v1Fail(w, r, 409, "pvf_conflict", err.Error(), false)
	case errors.Is(err, testbenefits.ErrOnlineSelection):
		v1Fail(w, r, 422, "invalid_online_snapshot", err.Error(), false)
	case errors.Is(err, testbenefits.ErrMailSelection):
		v1Fail(w, r, 422, "invalid_recipients", err.Error(), false)
	case errors.Is(err, sql.ErrNoRows):
		v1Fail(w, r, 404, "not_found", "记录不存在", false)
	case errors.Is(err, testbenefits.ErrRevision):
		v1Fail(w, r, 409, "revision_conflict", err.Error(), false)
	case errors.Is(err, testbenefits.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", err.Error(), false)
	case errors.Is(err, testbenefits.ErrCampaignDisabled):
		v1Fail(w, r, 409, "rule_disabled", err.Error(), false)
	case errors.Is(err, testbenefits.ErrRecipient):
		v1Fail(w, r, 422, "recipient_changed", err.Error(), false)
	case errors.Is(err, testbenefits.ErrPreviewEmpty), errors.Is(err, testbenefits.ErrPreviewLimit):
		v1Fail(w, r, 422, "preview_unavailable", err.Error(), false)
	default:
		if s.cfg.Logger != nil {
			s.cfg.Logger("gm-v1-operation-error", "request_id", r.Context().Value(v1RequestIDKey{}), "cause", err.Error())
		}
		v1Fail(w, r, 503, "operation_unavailable", "操作暂未完成，请刷新记录后重试", true)
	}
}

func (s *Server) v1OperationReply(w http.ResponseWriter, r *http.Request, data map[string]any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger("gm-v1-operation", "request_id", r.Context().Value(v1RequestIDKey{}), "operation", r.URL.Path, "resource_id", data["id"], "revision", data["revision"], "sent", data["sent"], "failed", data["failed"])
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}

func v1DeliveryFailure(message string) *v1Error {
	if message == "" {
		return nil
	}
	switch message {
	case "角色已删除，未发送", "角色已重新创建，请重新预览", "角色与收件账号不匹配", "账号不存在":
		return &v1Error{Code: "recipient_changed", Message: message, Retryable: false}
	default:
		return &v1Error{Code: "delivery_failed", Message: "投递失败，请检查服务状态后重试", Retryable: true}
	}
}

type v1RewardInput struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Gold   int64  `json:"gold"`
	ItemID string `json:"itemId"`
	Count  int64  `json:"count"`
}

func (s *Server) v1Reward(w http.ResponseWriter, r *http.Request, in v1RewardInput, scope testbenefits.GrantScope) (testbenefits.Job, string, bool) {
	j, name, err := s.v1ResolveReward(in, scope)
	if err != nil {
		s.v1OperationError(w, r, err)
	}
	return j, name, err == nil
}

func (s *Server) v1ResolveReward(in v1RewardInput, scope testbenefits.GrantScope) (testbenefits.Job, string, error) {
	if s.deps.Catalog != nil && in.ItemID != "" {
		var v catalogReward
		e := s.deps.Catalog.background(catalogRequest{Operation: "reward", Reward: in, Scope: scope}, &v)
		return v.Job, v.Name, e
	}
	j := testbenefits.Job{Title: strings.TrimSpace(in.Title), Body: in.Body, Gold: in.Gold, Scope: scope}
	name := ""
	if in.ItemID != "" {
		id, err := strconv.ParseInt(in.ItemID, 10, 32)
		if err != nil || id <= 0 {
			return j, name, &v1MailInputError{422, "invalid_item", "物品 ID 无效"}
		}
		j.Attachments = []repository.MailAttachment{{ItemID: id, Count: in.Count}}
	} else if in.Count != 0 {
		return j, name, &v1MailInputError{422, "invalid_item", "请填写物品 ID"}
	}
	if err := j.Validate(); err != nil {
		return j, name, &v1MailInputError{422, "invalid_reward", err.Error()}
	}
	if len(j.Attachments) > 0 {
		if s.items == nil || !s.items.isReady() || s.items.failure() != nil {
			return j, name, errors.New("item index unavailable")
		}
		entry, found := s.items.lookup(uint32(j.Attachments[0].ItemID))
		if !found || entry.Invalid {
			return j, name, &v1MailInputError{422, "invalid_item", "当前物品库没有可用的此物品"}
		}
		if entry.Kind == grantItemKindEquipment && in.Count != 1 {
			return j, name, &v1MailInputError{422, "invalid_count", "装备每封邮件数量为 1"}
		}
		j.Attachments[0].Extra = grantAttachmentExtra(entry, s.grantPVFDurability(entry))
		name = entry.Name
	}
	return j, name, nil
}
func v1RewardDTO(j testbenefits.Job) map[string]any {
	attachments := []map[string]any{}
	for _, a := range j.Attachments {
		attachments = append(attachments, v1MailAttachmentDTO(a))
	}
	return map[string]any{"title": j.Title, "body": j.Body, "gold": j.Gold, "cera": j.Cera, "attachments": attachments, "deliveryPlan": j.RewardPlan}
}
func v1JobDTO(j testbenefits.JobSummary) map[string]any {
	return map[string]any{"id": j.ID, "revision": j.Revision, "campaignId": j.CampaignID, "scope": j.Scope, "createdAt": j.CreatedAt, "state": j.State, "total": j.Total, "sent": j.Sent, "failed": j.Failed, "pending": j.Pending, "pvfChecksum": j.PVFChecksum, "recipientSource": j.RecipientSource, "reward": v1RewardDTO(testbenefits.Job{Title: j.Title, Body: j.Body, Gold: j.Gold, Cera: j.Cera, Attachments: j.Attachments, RewardPlan: j.RewardPlan})}
}
func v1CampaignDTO(c testbenefits.Campaign) map[string]any {
	return map[string]any{"id": c.ID, "revision": c.Revision, "scope": c.Reward.Scope.Effective(), "createdAt": c.CreatedAt, "firstEnabledAt": c.FirstEnabledAt, "enabled": c.Enabled, "autoNewCharacters": c.AutoNewCharacters, "autoExistingCharacters": c.AutoExistingCharacters, "itemName": c.ItemName, "pvfChecksum": c.Reward.PVFChecksum, "reward": v1RewardDTO(c.Reward)}
}
func (s *Server) v1CreateCampaign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		v1RewardInput
		Cera              int64                     `json:"cera"`
		Scope             testbenefits.GrantScope   `json:"scope"`
		AutoNewCharacters bool                      `json:"autoNewCharacters"`
		Options           *testbenefits.MailOptions `json:"options"`
		PVFChecksum       string                    `json:"pvfChecksum"`
	}
	if !v1Decode(w, r, &in) || !s.v1OperationReady(w, r) {
		return
	}
	key, ok := v1Idempotency(w, r)
	if !ok {
		return
	}
	if in.Scope != testbenefits.ScopeAccount && in.Scope != testbenefits.ScopeCharacter {
		v1Fail(w, r, 422, "invalid_scope", "请选择每账号一次或每角色一次", false)
		return
	}
	if in.Cera != 0 {
		j := testbenefits.Job{Title: strings.TrimSpace(in.Title), Body: in.Body, Cera: in.Cera, Scope: in.Scope}
		if in.Gold != 0 || in.ItemID != "" || in.Count != 0 || in.Options != nil || in.PVFChecksum != "" {
			v1Fail(w, r, 422, "invalid_reward", "点券请单独创建福利规则", false)
			return
		}
		if err := j.Validate(); err != nil {
			v1Fail(w, r, 422, "invalid_reward", err.Error(), false)
			return
		}
		c, err := s.deps.Benefits.CreateCampaignOnce(r.Context(), key, j, "", in.AutoNewCharacters)
		if err != nil {
			s.v1OperationError(w, r, err)
			return
		}
		s.v1OperationReply(w, r, v1CampaignDTO(c))
		return
	}
	if in.Options != nil || in.PVFChecksum != "" {
		draft := testbenefits.MailDraft{Title: in.Title, Body: in.Body, Gold: in.Gold, ItemID: in.ItemID, Count: in.Count, PVFChecksum: in.PVFChecksum}
		if in.Options != nil {
			draft.Options = *in.Options
		}
		c, err := s.deps.Benefits.CreateCampaignDraftOnce(r.Context(), key, in.Scope, in.AutoNewCharacters, draft, s.v1ProjectCampaignPlan)
		if err != nil {
			s.v1OperationError(w, r, err)
			return
		}
		s.v1OperationReply(w, r, v1CampaignDTO(c))
		return
	}
	j, name, ok := s.v1Reward(w, r, in.v1RewardInput, in.Scope)
	if !ok {
		return
	}
	c, err := s.deps.Benefits.CreateCampaignOnce(r.Context(), key, j, name, in.AutoNewCharacters)
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	s.v1OperationReply(w, r, v1CampaignDTO(c))
}
func (s *Server) v1MailPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		v1RewardInput
		AccountID   string                    `json:"accountId"`
		CharacterID string                    `json:"characterId"`
		Recipients  []testbenefits.MailTarget `json:"recipients"`
		AccountIDs  []string                  `json:"accountIds"`
		Options     *testbenefits.MailOptions `json:"options"`
		PVFChecksum string                    `json:"pvfChecksum"`
	}
	if !v1DecodeLimit(w, r, &in, 32<<10) || !s.v1OperationReady(w, r) {
		return
	}
	key, ok := v1Idempotency(w, r)
	if !ok {
		return
	}
	if in.Recipients != nil || in.AccountIDs != nil || in.Options != nil || in.PVFChecksum != "" {
		if (in.Recipients != nil || in.AccountIDs != nil) && (in.AccountID != "" || in.CharacterID != "") {
			v1Fail(w, r, 422, "invalid_recipients", "收件名单与单角色字段请选择一种", false)
			return
		}
		targets := in.Recipients
		if targets == nil && in.AccountIDs == nil {
			targets = []testbenefits.MailTarget{{AccountID: in.AccountID, CharacterID: in.CharacterID}}
		}
		draft := testbenefits.MailDraft{AccountIDs: in.AccountIDs, Recipients: targets, Title: in.Title, Body: in.Body, Gold: in.Gold, ItemID: in.ItemID, Count: in.Count, PVFChecksum: in.PVFChecksum}
		if in.Options != nil {
			draft.Options = *in.Options
		}
		g, err := s.v1OperationGroup()
		var j testbenefits.Job
		if err == nil {
			j, err = s.deps.Benefits.PreviewSelectedMailOnce(r.Context(), g, key, draft, s.v1ProjectMailDraft)
		}
		if err != nil {
			s.v1OperationError(w, r, err)
			return
		}
		s.v1OperationReply(w, r, v1JobDTO(testbenefits.Summarize(j)))
		return
	}
	if !v1ValidID(in.AccountID) || !v1ValidID(in.CharacterID) {
		v1Fail(w, r, 422, "invalid_recipient", "请选择账号与角色", false)
		return
	}
	j, _, ok := s.v1Reward(w, r, in.v1RewardInput, testbenefits.ScopeCharacter)
	if !ok {
		return
	}
	g, err := s.v1OperationGroup()
	if err == nil {
		j, err = s.deps.Benefits.PreviewMailOnce(r.Context(), g, key, in.AccountID, in.CharacterID, j)
	}
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	s.v1OperationReply(w, r, v1JobDTO(testbenefits.Summarize(j)))
}
func (s *Server) v1CampaignState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision *uint64 `json:"revision"`
		Enabled  *bool   `json:"enabled"`
		Confirm  bool    `json:"confirm"`
	}
	if !v1Decode(w, r, &in) || !s.v1OperationReady(w, r) {
		return
	}
	if in.Revision == nil || in.Enabled == nil || !in.Confirm {
		v1Fail(w, r, 422, "confirmation_required", "请核对规则并确认操作", false)
		return
	}
	c, err := s.deps.Benefits.EnableAutomaticCampaign(r.Context(), r.PathValue("id"), *in.Revision, *in.Enabled)
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	s.v1OperationReply(w, r, v1CampaignDTO(c))
}
func (s *Server) v1CampaignPreview(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !v1Decode(w, r, &in) || !s.v1OperationReady(w, r) {
		return
	}
	key, ok := v1Idempotency(w, r)
	if !ok {
		return
	}
	g, err := s.v1OperationGroup()
	var j testbenefits.Job
	if err == nil {
		j, err = s.deps.Benefits.PreviewCampaignOnce(r.Context(), g, r.PathValue("id"), key)
	}
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	s.v1OperationReply(w, r, v1JobDTO(testbenefits.Summarize(j)))
}
func (s *Server) v1RunTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision *uint64 `json:"revision"`
		Confirm  bool    `json:"confirm"`
		Retry    bool    `json:"retry"`
	}
	if !v1Decode(w, r, &in) || !s.v1OperationReady(w, r) {
		return
	}
	if !in.Confirm || in.Revision == nil {
		v1Fail(w, r, 422, "confirmation_required", "请核对任务版本并确认发放", false)
		return
	}
	g, err := s.v1OperationGroup()
	var j testbenefits.Job
	if err == nil {
		j, err = s.deps.Benefits.Job(r.Context(), r.PathValue("id"))
		if err == nil && j.CampaignID != "" && j.RewardPlan != nil {
			var c testbenefits.Campaign
			c, err = s.deps.Benefits.Campaign(r.Context(), j.CampaignID)
			if err == nil {
				j, err = testbenefits.AdoptCampaignReward(j, c)
			}
		}
		if err == nil && j.PVFChecksum != "" && j.PVFChecksum != s.v1PVFChecksum() {
			v1Fail(w, r, 409, "pvf_conflict", "该任务使用的 PVF 与当前版本不同。福利规则请先核对 PVF；单次邮件请重新准备名单", false)
			return
		}
	}
	if err == nil {
		j, err = s.deps.Benefits.RunVersionProjected(r.Context(), g, r.PathValue("id"), in.Retry, *in.Revision, s.v1ProjectCampaignDelivery)
	}
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	s.v1OperationReply(w, r, v1JobDTO(testbenefits.Summarize(j)))
}
func (s *Server) v1OperationPage(w http.ResponseWriter, r *http.Request, resource string) (v1Query, string, bool) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "请使用分页数量和游标查询", false)
		return q, "", false
	}
	after, err := s.v1CursorAfter(q.Cursor, resource, v1Filter(q, ""))
	if err != nil {
		v1Fail(w, r, 400, "invalid_cursor", "分页游标无效，请刷新列表", false)
		return q, "", false
	}
	return q, after, s.v1OperationReady(w, r)
}
func (s *Server) v1Campaigns(w http.ResponseWriter, r *http.Request) {
	q, after, ok := s.v1OperationPage(w, r, "benefits")
	if !ok {
		return
	}
	rows, next, err := s.deps.Benefits.CampaignPage(r.Context(), after, q.Limit)
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, c := range rows {
		out = append(out, v1CampaignDTO(c))
	}
	if next != "" {
		next = s.v1CursorToken("benefits", v1Filter(q, ""), next)
	}
	v1Reply(w, r, 200, v1Envelope{Data: out, Page: &v1Page{Limit: q.Limit, NextCursor: next}})
}
func (s *Server) v1Tasks(w http.ResponseWriter, r *http.Request) {
	q, after, ok := s.v1OperationPage(w, r, "tasks")
	if !ok {
		return
	}
	rows, next, err := s.deps.Benefits.JobPage(r.Context(), after, q.Limit)
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, j := range rows {
		out = append(out, v1JobDTO(j))
	}
	if next != "" {
		next = s.v1CursorToken("tasks", v1Filter(q, ""), next)
	}
	v1Reply(w, r, 200, v1Envelope{Data: out, Page: &v1Page{Limit: q.Limit, NextCursor: next}})
}
func (s *Server) v1Campaign(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	if !s.v1OperationReady(w, r) {
		return
	}
	c, err := s.deps.Benefits.Campaign(r.Context(), r.PathValue("id"))
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: v1CampaignDTO(c)})
}
func (s *Server) v1Task(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	if !s.v1OperationReady(w, r) {
		return
	}
	j, err := s.deps.Benefits.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	data := v1JobDTO(testbenefits.Summarize(j))
	if strings.HasSuffix(r.URL.Path, "/confirmation") {
		if len(j.Recipients) > testbenefits.MaxOnlineMailRecipients {
			s.v1OperationError(w, r, testbenefits.ErrPreviewLimit)
			return
		}
		rows := make([]map[string]string, 0, len(j.Recipients))
		for _, target := range j.Recipients {
			rows = append(rows, map[string]string{"accountId": target.AccountID, "characterId": target.CharacterID, "name": target.Name})
		}
		data["recipients"] = rows
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}
func (s *Server) v1Recipients(w http.ResponseWriter, r *http.Request) {
	resource := "recipients:" + r.PathValue("id")
	q, after, ok := s.v1OperationPage(w, r, resource)
	if !ok {
		return
	}
	offset := 0
	var err error
	if after != "" {
		offset, err = strconv.Atoi(after)
	}
	if err != nil || offset < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "分页游标无效", false)
		return
	}
	j, err := s.deps.Benefits.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		s.v1OperationError(w, r, err)
		return
	}
	end := min(len(j.Recipients), offset+q.Limit)
	if offset > len(j.Recipients) {
		v1Fail(w, r, 400, "invalid_cursor", "分页游标超出名单", false)
		return
	}
	out := []map[string]any{}
	for _, rec := range j.Recipients[offset:end] {
		status := "pending"
		if rec.MailID != "" {
			status = "sent"
		} else if rec.Error != "" {
			status = "failed"
		}
		var delivered any
		if rec.Delivery != nil {
			delivered = map[string]any{"grantedAt": rec.Delivery.GrantedAt, "pvfChecksum": rec.Delivery.PVFChecksum, "before": rec.Delivery.Before, "after": rec.Delivery.After, "reward": v1RewardDTO(testbenefits.Job{Cera: rec.Delivery.Cera, Attachments: rec.Delivery.Attachments})}
		}
		out = append(out, map[string]any{"delivery": delivered, "accountId": rec.AccountID, "characterId": rec.CharacterID, "name": rec.Name, "mailId": rec.MailID, "deliveredCharacterId": rec.DeliveredCharacterID, "state": status, "failure": v1DeliveryFailure(rec.Error)})
	}
	next := ""
	if end < len(j.Recipients) {
		next = s.v1CursorToken(resource, v1Filter(q, ""), strconv.Itoa(end))
	}
	v1Reply(w, r, 200, v1Envelope{Data: out, Page: &v1Page{Limit: q.Limit, NextCursor: next}})
}
