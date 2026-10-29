package gmtool

import (
	"net/http"
	"strconv"
	"time"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/moderation"
)

func (s *Server) v1Moderation(w http.ResponseWriter, r *http.Request) {
	if s.deps.Runtime == nil {
		v1Fail(w, r, 503, "moderation_unavailable", "游戏反作弊接口暂不可用", true)
		return
	}
	request := adminapi.Request{}
	if r.Method == http.MethodPost {
		if r.URL.RawQuery != "" {
			v1Fail(w, r, 400, "invalid_query", "封禁操作不接受查询参数", false)
			return
		}
		var c moderation.BanChange
		if !v1DecodeLimit(w, r, &c, 4096) {
			return
		}
		expires := c.ExpiresAt
		c.ExpiresAt = nil
		if err := c.Normalize(time.Now()); err != nil {
			v1Fail(w, r, 400, "invalid_ban", "请填写有效的目标、原因和请求编号", false)
			return
		}
		if !c.Revoke {
			c.ExpiresAt = expires
		}
		request.Action = "change_ban"
		request.BanChange = &c
	} else {
		q := moderation.Query{}
		params := r.URL.Query()
		valid := true
		for key, values := range params {
			if len(values) != 1 {
				valid = false
				break
			}
			switch key {
			case "sortBy":
				q.SortBy = values[0]
			case "sortOrder":
				q.SortOrder = values[0]
			case "search":
				q.Search = values[0]
			case "from", "to":
				v, err := time.Parse(time.RFC3339, values[0])
				if err != nil {
					valid = false
				}
				if key == "from" {
					q.From = v
				} else {
					q.To = v
				}
			case "offset", "limit", "dungeonId", "maxDurationMs", "minHourlyPeak":
				v, err := strconv.ParseInt(values[0], 10, 32)
				if err != nil {
					valid = false
				}
				switch key {
				case "offset":
					q.Offset = int(v)
				case "limit":
					q.Limit = int(v)
					if v < 1 {
						valid = false
					}
				case "dungeonId":
					q.DungeonID = v
				case "maxDurationMs":
					q.MaxDurationMS = v
				case "minHourlyPeak":
					q.MinHourlyPeak = v
				}
			default:
				valid = false
			}
		}
		if !valid || q.Normalize(time.Now()) != nil || q.NormalizeSort(r.PathValue("resource")) != nil {
			v1Fail(w, r, 400, "invalid_query", "请检查查询条件；时间范围最多 31 天", false)
			return
		}
		switch r.PathValue("resource") {
		case "clears":
			request.Action = "clear_stats"
		case "accounts":
			request.Action = "moderation_accounts"
		case "bans":
			request.Action = "bans"
		default:
			v1Fail(w, r, 404, "not_found", "接口不存在", false)
			return
		}
		request.ModerationQuery = &q
	}
	result, err := s.deps.Runtime.Call(r.Context(), request)
	if err != nil {
		if result.Error == "封禁状态已变化，请刷新后重新确认" {
			v1Fail(w, r, 409, "revision_conflict", result.Error, false)
			return
		}
		if result.Error == "参数无效或账号不存在" {
			v1Fail(w, r, 400, "invalid_ban", result.Error, false)
			return
		}
		diagnostics.Log(s.cfg.Logger, r.Context(), "gm-moderation-failed", err)
		v1Fail(w, r, 503, "moderation_unconfirmed", "暂未取得反作弊操作结果，可使用原请求重试", true)
		return
	}
	var data any
	switch request.Action {
	case "clear_stats":
		if result.Clears != nil {
			data = result.Clears
		}
	case "moderation_accounts":
		if result.ModerationAccounts != nil {
			data = result.ModerationAccounts
		}
	case "bans":
		if result.Bans != nil {
			data = result.Bans
		}
	case "change_ban":
		if result.Ban != nil {
			data = result.Ban
		}
	}
	if data == nil {
		v1Fail(w, r, 503, "moderation_unavailable", "游戏反作弊接口未返回结果", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: data})
}

func (s *Server) v1BanTargets(w http.ResponseWriter, r *http.Request) {
	if s.deps.Runtime == nil {
		v1Fail(w, r, 503, "moderation_unavailable", "反作弊服务暂不可用", true)
		return
	}
	out, err := s.deps.Runtime.Call(r.Context(), adminapi.Request{Action: "ban_targets", AccountID: r.PathValue("account")})
	if err != nil || out.BanTargets == nil {
		v1Fail(w, r, 503, "moderation_unavailable", "读取封禁目标失败，请重试", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: out.BanTargets})
}
