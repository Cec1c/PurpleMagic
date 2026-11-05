package gmtool

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/adminops"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1CurrencyAccount(w http.ResponseWriter, r *http.Request) (repository.Group, string, bool) {
	id := r.PathValue("accountId")
	if !v1ValidID(id) {
		v1Fail(w, r, 400, "invalid_account", "账号标识无效", false)
		return repository.Group{}, "", false
	}
	if _, err := s.v1LoadAccount(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			v1Fail(w, r, 404, "account_not_found", "账号不存在", false)
		} else {
			s.v1Unavailable(w, r, err)
		}
		return repository.Group{}, "", false
	}
	group, err := s.repositories()
	if err != nil || group.Account == nil {
		s.v1Unavailable(w, r, errAccountMissing)
		return group, "", false
	}
	return group, id, true
}

func (s *Server) v1Currency(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "此接口不接受查询参数", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	load := g.Account.Load
	if reader, ok := g.Account.(repository.AccountCurrencyReader); ok {
		load = reader.LoadAccountCurrency
	}
	a, found, err := load(r.Context(), id)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	if !found || a.AccountID != id {
		v1Fail(w, r, 404, "account_not_found", "账号存档不存在", false)
		return
	}
	balance, err := adminops.CurrencyBalance(a)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"accountId": id, "currency": "cera", "balance": strconv.FormatInt(balance, 10), "revision": adminops.CurrencyRevision(a)}})
}

func (s *Server) v1AdjustCurrency(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
		Revision string `json:"revision"`
		Reason   string `json:"reason"`
	}
	if !v1Decode(w, r, &body) {
		return
	}
	key, ok := v1Idempotency(w, r)
	if !ok {
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AccountManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	actor := "管理员"
	if s.cfg.LoginKeySHA256 != "" {
		actor = "管理凭据 " + s.cfg.LoginKeySHA256[:12]
	}
	result, err := adminops.AdjustCurrency(r.Context(), u, adminops.CurrencyCommand{AccountID: id, Currency: body.Currency, Amount: body.Amount, Revision: body.Revision, Reason: body.Reason, Actor: actor, Key: key})
	switch {
	case errors.Is(err, adminops.ErrConflict):
		v1Fail(w, r, 409, "revision_conflict", "余额已变更，请刷新后核对", false)
	case errors.Is(err, adminops.ErrIdempotency):
		v1Fail(w, r, 409, "idempotency_conflict", "此请求编号已用于其他操作", false)
	case errors.Is(err, adminops.ErrAmount):
		v1Fail(w, r, 422, "invalid_amount", "请检查点券数量、余额和操作原因", false)
	case errors.Is(err, adminops.ErrAccount) || errors.Is(err, sql.ErrNoRows):
		v1Fail(w, r, 404, "account_not_found", "账号存档不存在", false)
	case errors.Is(err, context.DeadlineExceeded):
		if s.cfg.Logger != nil {
			diagnostics.Log(s.cfg.Logger, r.Context(), "gm-currency-write-timeout", err)
		}
		v1Fail(w, r, 503, "currency_timeout", "点券修改等待超时，请使用原请求继续查询回执", true)
	case err != nil:
		s.v1Unavailable(w, r, err)
	default:
		v1Reply(w, r, 200, v1Envelope{Data: result})
	}
}

func (s *Server) v1CurrencyLog(w http.ResponseWriter, r *http.Request) {
	q, err := v1ParseQuery(r)
	if err != nil || q.Q != "" {
		v1Fail(w, r, 400, "invalid_query", "流水查询参数无效", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	u, ok := g.Account.(repository.AccountManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	filter := v1Filter(q, id)
	after, err := s.v1CursorAfter(q.Cursor, "currency-log", filter)
	var before int64
	if err == nil && after != "" {
		before, err = strconv.ParseInt(after, 10, 64)
	}
	if err != nil || before < 0 {
		v1Fail(w, r, 400, "invalid_cursor", "流水分页条件已变更", false)
		return
	}
	rows, err := u.AccountManagementLog(r.Context(), id, before, q.Limit+1)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	page := v1Page{Limit: q.Limit}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		page.NextCursor = s.v1CursorToken("currency-log", filter, strconv.FormatInt(rows[len(rows)-1].Sequence, 10))
	}
	data := []adminops.CurrencyReceipt{}
	for _, row := range rows {
		item, e := adminops.DecodeCurrencyReceipt(row.Payload)
		if e != nil {
			s.v1Unavailable(w, r, e)
			return
		}
		data = append(data, item)
	}
	v1Reply(w, r, 200, v1Envelope{Data: data, Page: &page})
}
