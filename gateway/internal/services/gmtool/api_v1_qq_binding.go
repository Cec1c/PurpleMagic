package gmtool

import (
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/qqbinding"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) v1QQBinding(w http.ResponseWriter, r *http.Request) {
	if s.deps.Runtime == nil {
		v1Fail(w, r, 503, "binding_unavailable", "绑定服务暂不可用", true)
		return
	}
	in := adminapi.Request{Action: "qq_settings"}
	if r.Method == http.MethodPost {
		var cfg qqbinding.Settings
		if !v1DecodeLimit(w, r, &cfg, 4096) {
			return
		}
		if err := cfg.Normalize(); err != nil {
			v1Fail(w, r, 400, "invalid_settings", err.Error(), false)
			return
		}
		in.Action = "qq_settings_save"
		in.QQSettings = &cfg
	} else if strings.HasSuffix(r.URL.Path, "/bindings") {
		q := qqbinding.Query{Search: r.URL.Query().Get("search"), Limit: 50}
		for k, v := range r.URL.Query() {
			if len(v) != 1 {
				v1Fail(w, r, 400, "invalid_query", "查询参数无效", false)
				return
			}
			if k == "offset" {
				n, err := strconv.Atoi(v[0])
				if err != nil || n < 0 {
					v1Fail(w, r, 400, "invalid_query", "查询参数无效", false)
					return
				}
				q.Offset = n
			} else if k != "search" {
				v1Fail(w, r, 400, "invalid_query", "查询参数无效", false)
				return
			}
		}
		in.Action = "qq_bindings"
		in.QQQuery = &q
	}
	out, err := s.deps.Runtime.Call(r.Context(), in)
	if err != nil {
		if out.Error != "" {
			v1Fail(w, r, 409, "binding_conflict", out.Error, false)
		} else {
			v1Fail(w, r, 503, "binding_unavailable", "绑定服务暂不可用", true)
		}
		return
	}
	if in.Action == "qq_bindings" {
		v1Reply(w, r, 200, v1Envelope{Data: out.QQBindings})
	} else {
		v1Reply(w, r, 200, v1Envelope{Data: out.QQSettings})
	}
}
