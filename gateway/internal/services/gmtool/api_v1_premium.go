package gmtool

import (
	"net/http"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/premium"
	"dnf90.local/gateway/internal/modules/dnf/repository"
)

func (s *Server) v1Premium(w http.ResponseWriter, r *http.Request) {
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	a, found, err := g.Account.Load(r.Context(), id)
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	if !found {
		v1Fail(w, r, 404, "account_not_found", "账号不存在", false)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"accountId": id, "type": premium.TypeBlackDiamond, "name": "黑钻贵族", "expireAt": premium.ExpireAt(a, premium.TypeBlackDiamond), "active": premium.Active(a, premium.TypeBlackDiamond, time.Now().UTC()), "accountShared": true, "reloginRequired": true}})
}

func (s *Server) v1GrantPremium(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Days int64 `json:"days"`
	}
	if !v1Decode(w, r, &body) {
		return
	}
	key, ok := v1Idempotency(w, r)
	if !ok {
		return
	}
	if body.Days < 1 || body.Days > 3650 {
		v1Fail(w, r, 422, "invalid_days", "请输入 1–3650 天", false)
		return
	}
	g, id, ok := s.v1CurrencyAccount(w, r)
	if !ok {
		return
	}
	types, err := s.premiumTypes()
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	registered := false
	for _, typ := range types {
		if typ == premium.TypeBlackDiamond {
			registered = true
		}
	}
	if !registered {
		v1Fail(w, r, 422, "premium_unavailable", "当前游戏资源未注册黑钻贵族", false)
		return
	}
	u, ok := g.Account.(repository.AccountManagementUnitOfWork)
	if !ok {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	var result premium.AdminGrantResult
	var grantErr error
	now := time.Now().UTC()
	err = u.WithinAccountManagement(r.Context(), id, func(accounts repository.AccountRepository, _ repository.ManagementReceiptStore) error {
		a, found, e := accounts.Load(r.Context(), id)
		if e != nil {
			return e
		}
		if !found {
			return errAccountMissing
		}
		next := repository.CloneAccount(a)
		result, grantErr = premium.GrantAdminAccountContract(&next, premium.TypeBlackDiamond, body.Days, key, now)
		if grantErr != nil {
			return grantErr
		}
		if result.Replayed {
			return nil
		}
		writer, ok := accounts.(repository.AccountMetadataEntryWriter)
		if !ok {
			return errRepositoriesUnavailable
		}
		// Only the contract expiry and its durable request receipt change.
		for k, value := range next.Metadata {
			if a.Metadata[k] != value {
				if e = writer.SaveMetadataEntry(r.Context(), id, k, value, now); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if grantErr != nil {
		v1Fail(w, r, 409, "premium_conflict", grantErr.Error(), false)
		return
	}
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{"kind": "premium-grant", "accountId": id, "type": premium.TypeBlackDiamond, "name": "黑钻贵族", "days": body.Days, "expireAt": result.ExpireAt, "active": result.ExpireAt > now.Unix(), "replayed": result.Replayed, "accountShared": true, "reloginRequired": true}})
}
