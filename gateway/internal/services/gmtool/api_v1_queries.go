package gmtool

import (
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	platformpvf "dnf90.local/gateway/internal/platform/pvf"
	"net/http"
)

func (s *Server) v1Activities(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "活动查询参数无效", false)
		return
	}
	if s.deps.Runtime != nil {
		result, err := s.deps.Runtime.Call(r.Context(), adminapi.Request{Action: "activities"})
		if err != nil || result.Activities == nil {
			v1Fail(w, r, 503, "activities_unavailable", "游戏活动接口暂不可用，请重试", true)
			return
		}
		v1Reply(w, r, 200, v1Envelope{Data: result.Activities})
		return
	}
	if s.deps.Activities == nil {
		v1Fail(w, r, 503, "activities_unavailable", "当前服务未接入活动查询", true)
		return
	}
	v1Reply(w, r, 200, v1Envelope{Data: s.deps.Activities.Snapshot()})
}

type pvfModeControl interface {
	ModeStatus() platformpvf.ModeStatus
	SetMode(string) error
}

func (s *Server) pvfModeControl() pvfModeControl {
	if s.deps.PVF == nil {
		return nil
	}
	source, err := s.deps.PVF()
	if err != nil {
		return nil
	}
	control, _ := source.(pvfModeControl)
	return control
}
func (s *Server) v1PVFMode(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "PVF 模式查询参数无效", false)
		return
	}
	control := s.pvfModeControl()
	if control == nil {
		v1Fail(w, r, 503, "pvf_mode_unavailable", "当前服务未接入 PVF 模式切换", false)
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			Mode string `json:"mode"`
		}
		if !v1Decode(w, r, &in) {
			return
		}
		if in.Mode != "disk" && in.Mode != "memory" {
			v1Fail(w, r, 400, "invalid_mode", "请选择磁盘索引或直接加载 PVF", false)
			return
		}
		if err := control.SetMode(in.Mode); err != nil {
			v1Fail(w, r, 503, "pvf_mode_failed", "PVF 模式保存失败，请重试", true)
			return
		}
	}
	v1Reply(w, r, 200, v1Envelope{Data: control.ModeStatus()})
}
