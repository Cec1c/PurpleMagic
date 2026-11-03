package launcherhub

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewsInteropHarness(t *testing.T) {
	path := os.Getenv("DNF90_HUB_NEWS_INTEROP_RECEIPT")
	if path == "" {
		t.Skip("news interop not requested")
	}
	h, _ := fixture(t)
	public := httptest.NewTLSServer(h.PublicHandler())
	defer public.Close()
	h.profile.ApiURL = public.URL
	h.profile.CertificateSHA256 = digest(public.Certificate().Raw)
	h.state.Published.Profile = h.profile
	cookie, csrf := auth(t, h, false)
	w := call(h.AdminHandler(), "PUT", "/admin/api/news", `[{"title":"跨语言公告","body":"## 第一行\n\n**第二行**","format":"markdown","category":"指南","date":"10 / 15"}]`, cookie, csrf, "https://admin.example.test")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	state := t.TempDir()
	profile := filepath.Join(state, "profile.json")
	raw, _ := json.Marshal(h.profile)
	if err := os.WriteFile(profile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]string{"profile": profile, "state": state})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Minute)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("news interop timed out")
		case <-ticker.C:
			if _, err := os.Stat(path + ".done"); err == nil {
				return
			}
		}
	}
}
