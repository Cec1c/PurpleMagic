package gmtool

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// NewManaged attaches the versioned API to a caller-owned listener and pool.
// It only opens prepared indexes; offline import is a separate executable.
func NewManaged(ctx context.Context, cfg Config, deps Deps) (*Server, error) {
	if deps.RegistrationDB == nil {
		return nil, errors.New("management requires a shared registration pool")
	}
	s, err := newServer(cfg, deps, true)
	if err != nil {
		return nil, err
	}
	if err = s.db.PingContext(ctx); err != nil {
		return nil, errors.New("registration database unavailable")
	}
	if err = s.items.loadCache(); err != nil {
		// Directory/character queries remain available while assets are prepared.
		s.cfg.Logger("management-item-index-unavailable")
	}
	return s, nil
}

func managementOriginAllowed(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return err == nil && u.Scheme == scheme && u.Host == r.Host && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
	}
	return true
}

// ManagementHandler deliberately registers only the versioned management API.
// The inherited legacy mutations are migration sources, not exposed routes.
func (s *Server) ManagementHandler(webRoot string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", s.v1Routes())
	mux.Handle("/api/v1", s.v1Routes())
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"configured": true, "authenticated": s.authenticated(r), "toolVersion": ToolVersion})
	})
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	if webRoot != "" {
		mux.Handle("GET /console/", http.StripPrefix("/console/", http.FileServer(http.Dir(webRoot))))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.Method == http.MethodGet {
			http.Redirect(w, r, "/console/#players", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !managementOriginAllowed(r) {
			http.Error(w, "来源验证失败", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/console/") {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		}
		mux.ServeHTTP(w, r)
	})
}
