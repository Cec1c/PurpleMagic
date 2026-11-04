package gmtool

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dnf90.local/gateway/internal/servicehttp"
	"dnf90.local/gateway/internal/services/launcherhub"
)

func TestExternalHubSingleSession(t *testing.T) {
	for _, mode := range []string{"external", "integrated"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
			priv, _ := x509.MarshalPKCS8PrivateKey(key)
			publicKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
			profile := launcherhub.Profile{ApiURL: "https://launcher.example.test", CertificateSHA256: strings.Repeat("a", 64), UpdatePublicKey: publicKey, ExeSHA256: strings.Repeat("b", 64), PvfSHA256: strings.Repeat("c", 64), GameHost: "192.0.2.1", BootstrapPort: 7001}
			raw, _ := json.Marshal(profile)
			if err := os.WriteFile(filepath.Join(root, "profile.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}), 0600); err != nil {
				t.Fatal(err)
			}
			hub, err := launcherhub.New(launcherhub.Config{AdminListen: "127.0.0.1:8444", AdminTokenSHA256: strings.Repeat("d", 64), DataDir: filepath.Join(root, "state"), SigningKeyFile: filepath.Join(root, "key.pem"), ProfileFile: filepath.Join(root, "profile.json")})
			if err != nil {
				t.Fatal(err)
			}
			serviceToken, hash := strings.Repeat("s", 48), strings.Repeat("c", 64)
			private := hub.GMHandler(serviceToken, hash)
			upstream := httptest.NewServer(private)
			defer upstream.Close()
			peer, err := servicehttp.New(servicehttp.Config{URL: upstream.URL, Token: serviceToken, PVFSHA256: hash})
			if err != nil {
				t.Fatal(err)
			}
			s := &Server{cfg: Config{Password: strings.Repeat("p", 48), Logger: func(string, ...any) {}}, sessions: map[string]time.Time{}, failures: map[string][]time.Time{}}
			h := s.ExternalHandler("", "https://gm.example.test", peer)
			if mode == "integrated" {
				mux := http.NewServeMux()
				mux.Handle("/api/v1/hub/", hub.ConsoleHandler(s.HubHandler))
				mux.Handle("/", s.ManagementHandler(""))
				h = mux
			}

			var cookie *http.Cookie
			csrf := ""
			request := func(method, path string, body any, revision, id string) *httptest.ResponseRecorder {
				raw, _ := json.Marshal(body)
				r := httptest.NewRequest(method, "https://gm.example.test"+path, strings.NewReader(string(raw)))
				if cookie != nil {
					r.AddCookie(cookie)
				}
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "https://gm.example.test")
				r.Header.Set("X-CSRF-Token", csrf)
				r.Header.Set("Idempotency-Key", id)
				if revision != "" {
					r.Header.Set("If-Match", fmt.Sprintf("%q", revision))
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			assertCode := func(w *httptest.ResponseRecorder, code int) map[string]any {
				t.Helper()
				if w.Code != code {
					t.Fatalf("status %d, want %d: %s", w.Code, code, w.Body.String())
				}
				var data map[string]any
				json.Unmarshal(w.Body.Bytes(), &data)
				if len(w.Result().Cookies()) != 0 {
					t.Fatal("Hub issued a second browser session")
				}
				return data
			}
			assertCode(request("GET", "/api/v1/hub/state", nil, "", ""), 401)
			login := request("POST", "/api/auth/login", map[string]any{"password": s.cfg.Password}, "", "")
			if login.Code != 200 || len(login.Result().Cookies()) != 1 {
				t.Fatal("GM login failed", login.Body.String())
			}
			cookie = login.Result().Cookies()[0]
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(cookie)
			csrf = s.v1CSRF(r)
			state := assertCode(request("GET", "/api/v1/hub/state", nil, "", ""), 200)
			revision := state["newsRevision"].(string)
			body := map[string]any{"action": "save", "news": map[string]any{"title": "一次登录", "body": "## 公告", "format": "markdown", "category": "综合"}}
			oldCSRF := csrf
			csrf = ""
			assertCode(request("POST", "/api/v1/hub/news/entries", body, revision, "first-news"), 403)
			assertCode(request("PUT", "/api/v1/hub/connection", map[string]any{}, "", ""), 403)
			csrf = oldCSRF
			assertCode(request("POST", "/api/v1/hub/news/entries", body, "", "first-news"), 409)
			result := assertCode(request("POST", "/api/v1/hub/news/entries", body, revision, "first-news"), 200)
			assertCode(request("POST", "/api/v1/hub/news/entries", body, revision, "first-news"), 200)
			assertCode(request("POST", "/api/v1/hub/news/entries", body, revision, "another-news"), 409)
			page := assertCode(request("GET", "/api/v1/hub/news?limit=20&q="+"%E4%B8%80", nil, "", ""), 200)
			rows := page["rows"].([]any)
			if len(rows) != 1 {
				t.Fatal("publication/replay/search failed", page)
			}
			assertCode(request("POST", "/api/v1/hub/news/entries", map[string]any{"action": "delete", "id": rows[0].(map[string]any)["id"]}, result["newsRevision"].(string), "delete-news"), 200)
			assertCode(request("PUT", "/api/v1/hub/source", map[string]any{"id": "primary", "name": "测试源", "endpoint": "https://download.example.test", "publicKey": publicKey, "enabled": true}, "", ""), 200)
			assertCode(request("PUT", "/api/v1/hub/connection", map[string]any{"gameHost": "192.0.2.2", "bootstrapPort": 7002}, state["connectionRevision"].(string), ""), 200)
			for _, path := range []string{"login", "logout", "publish", "bundle/stable", "check"} {
				assertCode(request("POST", "/api/v1/hub/"+path, map[string]any{}, "", ""), 404)
			}
			for _, origin := range []string{"https://evil.example.test", "https://gm.example.test.evil.test"} {
				r := httptest.NewRequest("PUT", "https://gm.example.test/api/v1/hub/source", strings.NewReader("{}"))
				r.AddCookie(cookie)
				r.Header.Set("Origin", origin)
				r.Header.Set("X-CSRF-Token", csrf)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				assertCode(w, 403)
			}
			for _, handler := range []http.Handler{private, hub.AdminHandler(), hub.PublicHandler()} {
				r := httptest.NewRequest("GET", "/api/internal/hub/state", nil)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code == 200 {
					t.Fatal("browser cookie reached a non-GM surface")
				}
			}
			for _, path := range []string{"publish", "bundle/stable", "login"} {
				res, err := peer.Do(t.Context(), "POST", "/api/internal/hub/"+path, []byte("{}"), nil)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != 404 {
					t.Fatal("private route escaped allowlist", path)
				}
			}
			logout := request("POST", "/api/auth/logout", map[string]any{}, "", "")
			if logout.Code != 200 {
				t.Fatal(logout.Body.String())
			}
			assertCode(request("GET", "/api/v1/hub/state", nil, "", ""), 401)
			assertCode(request("POST", "/api/v1/hub/news/entries", body, revision, "after-logout"), 401)

		})
	}
}
