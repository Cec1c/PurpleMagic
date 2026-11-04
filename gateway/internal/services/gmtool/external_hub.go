package gmtool

import (
	"io"
	"net/http"
	"strings"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/hubapi"
	"dnf90.local/gateway/internal/servicehttp"
)

// Called only after ExternalHandler validates the GM session and origin.
func (s *Server) externalHub(w http.ResponseWriter, r *http.Request, peer *servicehttp.Client) {
	route := strings.TrimPrefix(r.URL.Path, hubapi.BrowserPrefix)
	if !hubapi.Allowed(r.Method, route) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && !s.v1CheckWrite(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		v1Fail(w, r, 413, "body_too_large", "请求内容过大", false)
		return
	}
	path := hubapi.PrivatePrefix + route
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	res, err := peer.Do(r.Context(), r.Method, path, raw, r.Header)
	if err != nil {
		diagnostics.Log(s.cfg.Logger, r.Context(), "gm-hub-failed", err)
		v1Fail(w, r, 503, "hub_unavailable", "公告与下载源暂时无法连接，请重试", true)
		return
	}
	defer res.Body.Close()
	if res.Header.Get("X-PurpleMagic-Protocol") != servicehttp.Version || res.Header.Get("X-PVF-SHA256") != peer.Hash() {
		v1Fail(w, r, 503, "hub_unavailable", "公告与下载源服务需要更新或检查配置", true)
		return
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, servicehttp.MaxResponse+1))
	if err != nil || len(raw) > servicehttp.MaxResponse {
		v1Fail(w, r, 503, "hub_response", "公告与下载源响应读取失败，请重试", true)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(res.StatusCode)
	w.Write(raw)
}

// HubHandler binds integrated Hub operations to the same GM browser session.
func (s *Server) HubHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.authenticated(r) {
			v1Fail(w, r, 401, "authentication_required", "请先登录管理工作台", false)
			return
		}
		if !managementOriginAllowed(r) {
			v1Fail(w, r, 403, "origin_required", "来源验证失败", false)
			return
		}
		if !strings.HasPrefix(r.URL.Path, hubapi.BrowserPrefix) || !hubapi.Allowed(r.Method, strings.TrimPrefix(r.URL.Path, hubapi.BrowserPrefix)) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && !s.v1CheckWrite(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}
