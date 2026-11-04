package launcherhub

import (
	"net/http"
	"strings"

	"dnf90.local/gateway/internal/hubapi"
	"dnf90.local/gateway/internal/servicehttp"
)

// GMHandler uses the private service identity after GMService has checked the
// browser session, origin and CSRF. It never exposes release/upload operations.
func (h *Hub) GMHandler(token, hash string) http.Handler {
	return servicehttp.Protect(token, hash, h.gmOperations(hubapi.PrivatePrefix))
}

// ConsoleHandler mounts the same operations behind the integrated GM session guard.
func (h *Hub) ConsoleHandler(guard func(http.Handler) http.Handler) http.Handler {
	if guard == nil {
		return http.NotFoundHandler()
	}
	return guard(h.gmOperations(hubapi.BrowserPrefix))
}

func (h *Hub) gmOperations(prefix string) http.Handler {
	admin := h.adminHandler(func(*http.Request) (string, session, bool) {
		return "bearer", session{}, true
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasPrefix(r.URL.Path, prefix) || !hubapi.Allowed(r.Method, route) {
			http.NotFound(w, r)
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/admin/api/" + route
		r.URL.RawPath = ""
		admin.ServeHTTP(w, r)
	})
}
