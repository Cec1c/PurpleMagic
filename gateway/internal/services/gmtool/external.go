package gmtool

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"dnf90.local/gateway/internal/servicehttp"
)

func staticCatalogRoute(path string) bool {
	return path == "/api/v1/items" || strings.HasPrefix(path, "/api/v1/items/") || path == "/api/v1/quests" || strings.HasPrefix(path, "/api/v1/quests/")
}

func localGMRoute(path string) bool {
	return staticCatalogRoute(path) || path == "/api/v1/settings/pvf"
}

// GatewayHandler is mounted only on the private management listener. Browser
// credentials and legacy routes are absent in this deployment mode.
func (s *Server) GatewayHandler(token, hash string) http.Handler {
	api := s.v1Routes()
	var scan sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/gateway/v1/scan-v2", func(w http.ResponseWriter, r *http.Request) {
		var in benefitScanPosition
		if !v1Decode(w, r, &in) {
			return
		}
		if in.Account < 0 || len(in.Character) > 128 || len(in.Campaign) > 32 || (in.Character == "") != (in.Campaign == "") {
			http.Error(w, "scan position invalid", 400)
			return
		}
		if !scan.TryLock() {
			http.Error(w, "scan in progress", 409)
			return
		}
		defer scan.Unlock()
		ctx, stop := context.WithTimeout(r.Context(), 20*time.Second)
		defer stop()
		position, err := s.scanBenefitDeliveries(ctx, in)
		if err != nil {
			s.cfg.Logger("benefit-scan-retry", "account_cursor", position.Account, "cause", err.Error())
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Position benefitScanPosition `json:"position"`
			Retry    bool                `json:"retry"`
		}{position, err != nil})
	})
	mux.HandleFunc("POST /api/internal/gateway/v1/scan", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Cursor int64 `json:"cursor"`
			Limit  int   `json:"limit"`
		}
		if !v1Decode(w, r, &in) {
			return
		}
		if in.Cursor < 0 || in.Limit < 1 || in.Limit > 5 {
			http.Error(w, "scan range invalid", 400)
			return
		}
		if !scan.TryLock() {
			http.Error(w, "scan in progress", 409)
			return
		}
		defer scan.Unlock()
		ctx, stop := context.WithTimeout(r.Context(), 20*time.Second)
		defer stop()
		cursor, e := s.ScanNewCharacters(ctx, in.Cursor, in.Limit)
		out := struct {
			Cursor int64 `json:"cursor"`
			Retry  bool  `json:"retry"`
		}{cursor, e != nil}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	mux.Handle("/api/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if localGMRoute(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		api.ServeHTTP(w, r)
	}))
	return servicehttp.Protect(token, hash, mux)
}

// ExternalHandler owns browser authentication, static catalog queries, and the
// private peer identity. Only typed v1 routes reach the game-side gateway.
func (s *Server) ExternalHandler(webRoot, publicOrigin string, peer *servicehttp.Client, queues ...*ExternalQueue) http.Handler {
	var queue *ExternalQueue
	if len(queues) > 0 {
		queue = queues[0]
	}
	base := s.ManagementHandler(webRoot)
	catalog := s.v1Routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// This listener is loopback-only behind the configured HTTPS origin.
		if publicOrigin != "" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != publicOrigin {
				http.Error(w, "来源验证失败", 403)
				return
			}
			if strings.HasPrefix(publicOrigin, "https://") {
				r = r.Clone(r.Context())
				r.TLS = &tls.ConnectionState{}
			}
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			base.ServeHTTP(w, r)
			return
		}
		var requestID [16]byte
		if _, e := rand.Read(requestID[:]); e != nil {
			v1Fail(w, r, 503, "unavailable", "服务暂不可用", true)
			return
		}
		id := hex.EncodeToString(requestID[:])
		r = r.WithContext(context.WithValue(r.Context(), v1RequestIDKey{}, id))
		w.Header().Set("X-Request-ID", id)
		if !s.authenticated(r) {
			v1Fail(w, r, 401, "authentication_required", "请先登录管理工作台", false)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" || !managementOriginAllowed(r) {
			v1Fail(w, r, 403, "origin_required", "来源验证失败", false)
			return
		}
		if localGMRoute(r.URL.Path) {
			catalog.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			v1Fail(w, r, 405, "method_not_allowed", "此接口不支持该请求方法", false)
			return
		}
		if r.Method == http.MethodPost && !s.v1CheckWrite(w, r) {
			return
		}
		if queue != nil && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") && queue.Handle(w, r) {
			return
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil {
			v1Fail(w, r, 413, "body_too_large", "请求内容过大", false)
			return
		}
		taskSchedule := ""
		if queue != nil && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
			if queueID(id) {
				taskSchedule = queue.Status(id)
			}
		}
		res, e := peer.Do(r.Context(), r.Method, r.URL.RequestURI(), raw, r.Header)
		if e != nil {
			v1Fail(w, r, 503, "gateway_unavailable", "游戏网关连接暂不可用", true)
			return
		}
		defer res.Body.Close()
		if res.Header.Get("X-PurpleMagic-Protocol") != servicehttp.Version || res.Header.Get("X-PVF-SHA256") != peer.Hash() {
			v1Fail(w, r, 503, "gateway_generation", "游戏网关与 PVF 代次不一致", true)
			return
		}
		raw, e = io.ReadAll(io.LimitReader(res.Body, servicehttp.MaxResponse+1))
		if e != nil || len(raw) > servicehttp.MaxResponse {
			v1Fail(w, r, 503, "gateway_response", "游戏网关响应读取失败", true)
			return
		}
		if r.URL.Path == "/api/v1/capabilities" && res.StatusCode == 200 {
			var v map[string]any
			if json.Unmarshal(raw, &v) != nil {
				v1Fail(w, r, 503, "gateway_response", "游戏网关响应读取失败", true)
				return
			}
			data, ok := v["data"].(map[string]any)
			if !ok {
				v1Fail(w, r, 503, "gateway_response", "游戏网关响应读取失败", true)
				return
			}
			data["csrfToken"] = s.v1CSRF(r)
			data["deployment"] = "external-gm-v1"
			data["backgroundMail"] = queue != nil
			if features, ok := data["features"].(map[string]any); ok {
				features["pvf.mode"] = s.pvfModeControl() != nil
			}
			raw, _ = json.Marshal(v)
		}
		if queue != nil && res.StatusCode == 200 && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
			if queueID(id) {
				var v map[string]any
				if json.Unmarshal(raw, &v) == nil {
					if d, ok := v["data"].(map[string]any); ok {
						d["schedule"] = taskSchedule
						raw, _ = json.Marshal(v)
					}
				}
			}
		}
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.Header().Set("X-Request-ID", res.Header.Get("X-Request-ID"))
		w.WriteHeader(res.StatusCode)
		w.Write(raw)
	})
}

// RunExternalScanner drives bounded gateway transactions. Cursor is disposable;
// restart recovery always relies on the original permanent delivery receipts.
func RunExternalScanner(ctx context.Context, peer *servicehttp.Client, interval time.Duration, log func(string, ...any)) error {
	if interval < 5*time.Second {
		return errors.New("scan interval must be at least five seconds")
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	var position benefitScanPosition
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		for i := 0; i < 10; i++ {
			var out struct {
				Position benefitScanPosition `json:"position"`
				Retry    bool                `json:"retry"`
			}
			e := peer.JSON(ctx, "/api/internal/gateway/v1/scan-v2", position, &out)
			if e != nil {
				if ctx.Err() == nil {
					log("external-benefit-scan-retry")
				}
				break
			}
			position = out.Position
			if out.Retry {
				log("external-benefit-scan-retry", "account_cursor", position.Account, "character_id", position.Character, "campaign_id", position.Campaign)
			}
			if position.Account == 0 || out.Retry {
				break
			}
		}
		timer.Reset(interval)
	}
}
