package gmtool

import (
	"net/http"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
)

func (s *Server) v1ResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || !v1ValidID(r.PathValue("accountId")) || len(r.PathValue("accountId")) > 64 {
		v1Fail(w, r, 400, "invalid_reset", "重置参数无效", false)
		return
	}
	var in adminapi.PasswordReset
	if !v1DecodeLimit(w, r, &in, 2048) {
		return
	}
	if in.Validate() != nil {
		v1Fail(w, r, 400, "invalid_reset", "重置参数无效", false)
		return
	}
	if s.deps.Runtime == nil {
		v1Fail(w, r, 503, "reset_unavailable", "密码重置服务暂不可用", true)
		return
	}
	// Do not use the general operation store: its request/result persistence is
	// intended for game assets, not credentials. The game owns reset receipts.
	out, err := s.deps.Runtime.Call(r.Context(), adminapi.Request{Action: "reset_account_password", AccountID: r.PathValue("accountId"), PasswordReset: &in})
	if err != nil {
		switch out.ErrorCode {
		case "account_changed", "reset_conflict", "reset_superseded":
			v1Fail(w, r, 409, out.ErrorCode, out.Error, false)
		case "invalid_reset":
			v1Fail(w, r, 400, out.ErrorCode, "重置参数无效", false)
		default:
			v1Fail(w, r, 503, "reset_unconfirmed", "暂未取得重置结果，请保留此窗口并重试", true)
		}
		return
	}
	result := out.PasswordReset
	if result == nil || result.AccountID != r.PathValue("accountId") || result.Account != in.Account || result.RequestID != in.RequestID || result.ResetAt <= 0 {
		v1Fail(w, r, 503, "reset_unconfirmed", "暂未取得重置结果，请保留此窗口并重试", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: result})
}
