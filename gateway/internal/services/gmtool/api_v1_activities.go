package gmtool

import (
	"context"
	"errors"
	"net/http"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/onlineevent"
)

func (s *Server) activitySnapshot(ctx context.Context) (*onlineevent.ControlSnapshot, error) {
	done := diagnostics.Start(ctx, "activities.read")
	if s.deps.Runtime != nil {
		result, err := s.deps.Runtime.Call(ctx, adminapi.Request{Action: "activities"})
		if err == nil && result.Activities == nil {
			err = errors.New("missing activity snapshot")
		}
		done(err)
		return result.Activities, err
	}
	if s.deps.Activities == nil {
		err := errors.New("activity controls unavailable")
		done(err)
		return nil, err
	}
	result := s.deps.Activities.Snapshot()
	done(nil)
	return &result, nil
}

// The game process owns activity definitions, persistence and the revision CAS.
// Saving configuration does not restart the game or alter its active catalog.
func (s *Server) v1SaveActivities(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		v1Fail(w, r, 400, "invalid_query", "活动保存不接受查询参数", false)
		return
	}
	var in struct {
		Revision   *uint64 `json:"revision"`
		Activities []struct {
			ID      uint16 `json:"id"`
			Enabled *bool  `json:"enabled"`
		} `json:"activities"`
	}
	if !v1DecodeLimit(w, r, &in, 16<<10) {
		return
	}
	if in.Revision == nil || len(in.Activities) == 0 || len(in.Activities) > 256 {
		v1Fail(w, r, 400, "invalid_activities", "请提供当前配置版本和 1–256 个活动", false)
		return
	}
	changes := make([]onlineevent.ControlChange, 0, len(in.Activities))
	seen := make(map[uint16]bool, len(in.Activities))
	for _, row := range in.Activities {
		if row.ID == 0 || row.Enabled == nil || seen[row.ID] {
			v1Fail(w, r, 400, "invalid_activities", "活动 ID 须唯一，并明确指定开启或关闭", false)
			return
		}
		seen[row.ID] = true
		changes = append(changes, onlineevent.ControlChange{ID: row.ID, Enabled: *row.Enabled})
	}
	current, err := s.activitySnapshot(r.Context())
	if err != nil {
		v1Fail(w, r, 503, "activities_unavailable", "游戏活动暂时无法读取，请重试", true)
		return
	}
	if current.Revision != *in.Revision {
		v1Fail(w, r, 409, "revision_conflict", "活动配置已更新，请刷新后重新确认", false)
		return
	}
	known := make(map[uint16]bool, len(current.Activities))
	for _, row := range current.Activities {
		known[row.ID] = row.Enabled
	}
	changed := make([]onlineevent.ControlChange, 0, len(changes))
	for _, row := range changes {
		enabled, ok := known[row.ID]
		if !ok {
			v1Fail(w, r, 400, "unsupported_activity", "所选活动已不在当前游戏活动目录中，请刷新", false)
			return
		}
		if enabled != row.Enabled {
			changed = append(changed, row)
		}
	}
	if len(changed) == 0 {
		v1Reply(w, r, 200, v1Envelope{Data: current})
		return
	}
	changes = changed
	done := diagnostics.Start(r.Context(), "activities.save")
	var saved *onlineevent.ControlSnapshot
	if s.deps.Runtime != nil {
		var response adminapi.Response
		response, err = s.deps.Runtime.Call(r.Context(), adminapi.Request{Action: "save_activities", Revision: *in.Revision, Changes: changes})
		saved = response.Activities
	} else {
		var result onlineevent.ControlSnapshot
		result, err = s.deps.Activities.Save(r.Context(), *in.Revision, changes)
		saved = &result
	}
	if err == nil && saved == nil {
		err = errors.New("missing saved activity snapshot")
	}
	done(err)
	if err != nil {
		// A concurrent operator or a lost successful reply can advance revision.
		// Return a conflict and let the caller read the exact requested states.
		if latest, readErr := s.activitySnapshot(r.Context()); readErr == nil && latest.Revision != *in.Revision {
			v1Fail(w, r, 409, "revision_conflict", "活动配置版本已变化，请回读当前状态", false)
			return
		}
		diagnostics.Log(s.cfg.Logger, r.Context(), "gm-activity-save-failed", err)
		v1Fail(w, r, 503, "activity_save_unconfirmed", "尚未取得活动保存结果，请继续查询状态", true)
		return
	}
	diagnostics.Log(s.cfg.Logger, r.Context(), "gm-activity-settings-saved", nil, "revision", saved.Revision, "changes", len(changes), "restart_required", saved.RestartRequired)
	v1Reply(w, r, 200, v1Envelope{Data: saved})
}
