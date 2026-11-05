package gmtool

import (
	"context"
	"net/http"
	"strings"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/repository"
)

const systemSettingsScope = "purplemagic:management:system"

type systemSettings struct {
	CarbonMode bool `json:"carbonMode"`
}
type carbonModeKey struct{}

func carbonMode(ctx context.Context) bool {
	enabled, _ := ctx.Value(carbonModeKey{}).(bool)
	return enabled
}

func (s *Server) loadSystemSettings(ctx context.Context) (systemSettings, error) {
	if s.deps.Repos == nil {
		return systemSettings{}, nil
	}
	g, available := s.deps.Repos()
	if !available {
		return systemSettings{}, errRepositoriesUnavailable
	}
	if g.Settings == nil {
		return systemSettings{}, nil
	}
	v, _, err := g.Settings.Load(ctx, systemSettingsScope)
	return systemSettings{CarbonMode: v.Values["carbonMode"] == "true"}, err
}

func (s *Server) v1SystemSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		v, err := s.loadSystemSettings(r.Context())
		if err != nil {
			s.v1Unavailable(w, r, err)
			return
		}
		v1Reply(w, r, 200, v1Envelope{Data: v})
		return
	}
	var in struct {
		CarbonMode *bool `json:"carbonMode"`
	}
	if !v1Decode(w, r, &in) {
		return
	}
	if in.CarbonMode == nil {
		v1Fail(w, r, 400, "invalid_settings", "请选择碳铀模式的开关状态", false)
		return
	}
	g, err := s.repositories()
	if err != nil || g.Settings == nil {
		s.v1Unavailable(w, r, errRepositoriesUnavailable)
		return
	}
	value := "false"
	if *in.CarbonMode {
		value = "true"
	}
	// A dedicated scope survives service upgrades without touching game settings.
	err = g.Settings.Save(r.Context(), repository.SettingsRecord{Scope: systemSettingsScope, Values: map[string]string{"carbonMode": value}, UpdatedAt: time.Now().UTC()})
	if err != nil {
		s.v1Unavailable(w, r, err)
		return
	}
	if s.cfg.Logger != nil {
		s.cfg.Logger("gm-system-settings", "carbon_mode", *in.CarbonMode)
	}
	v1Reply(w, r, 200, v1Envelope{Data: systemSettings{CarbonMode: *in.CarbonMode}})
}

// Read the policy once, before taking asset locks. Every offline check in the
// transaction observes the same mode; no runtime call or kick occurs in carbon mode.
func systemPolicyRoute(path string) bool {
	for _, family := range []string{"inventory-operations", "quest-operations", "rename-operations", "adventure-rename-operations", "container-operations", "expert-job-operations", "gold-operations", "level-operations", "mailbox-cleanups", "mailbox-operations", "profession-operations", "skill-point-operations", "vault-operations"} {
		if strings.Contains(path, "/"+family+"/") || strings.HasSuffix(path, "/"+family) {
			return true
		}
	}
	return false
}
