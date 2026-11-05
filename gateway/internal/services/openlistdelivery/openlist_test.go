package openlistdelivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredTianyiDelivery(t *testing.T) {
	hash := strings.Repeat("a", 64)
	calls := 0
	body := map[string]any{"code": 200, "data": map[string]any{"size": 123, "name": hash + ".zip", "raw_url": "https://cloudcube.wuxi.cn/object?X-Amz-Signature=fixture", "header": "", "is_dir": false}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/fs/get" || r.Method != "POST" || r.Header.Get("Authorization") != "private-fixture" {
			t.Error("private API request mismatch")
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["path"] != "/release/"+hash+".zip" {
			t.Error("unregistered path")
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	registry := filepath.Join(dir, "artifacts.json")
	os.WriteFile(token, []byte("private-fixture"), 0600)
	b, _ := json.Marshal(map[string]Artifact{hash: {"/release/" + hash + ".zip", 123}})
	os.WriteFile(registry, b, 0600)
	cfg := Config{srv.URL, token, registry}
	r, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	for _, cap := range []string{"", "baidu-v1", "unknown"} {
		req := httptest.NewRequest("GET", "https://gateway.test/blobs/"+hash, nil)
		req.Header.Set(CapabilityHeader, cap)
		if r.Redirect(httptest.NewRecorder(), req, hash, 123) {
			t.Fatal("legacy client redirected")
		}
	}
	if calls != 0 {
		t.Fatal("legacy clients queried provider")
	}
	if _, e = r.Resolve(context.Background(), hash, 124, false); e == nil || calls != 0 {
		t.Fatal("size mismatch reached provider")
	}
	for _, fresh := range []bool{false, false, true} {
		req := httptest.NewRequest("GET", "https://gateway.test/blobs/"+hash, nil)
		req.Header.Set(CapabilityHeader, Capability)
		if fresh {
			req.Header.Set("X-DNF90-Refresh-Link", "1")
		}
		w := httptest.NewRecorder()
		if !r.Redirect(w, req, hash, 123) || w.Code != 307 || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Header().Get("Vary"), CapabilityHeader) {
			t.Fatal("redirect contract failed")
		}
	}
	if calls != 2 {
		t.Fatal("fresh resolver did not bypass cache", calls)
	}
	data := body["data"].(map[string]any)
	for _, bad := range []string{"http://cloudcube.wuxi.cn/x", "https://cloudcube.wuxi.cn.evil.test/x", "https://cloudcube.wuxi.cn:444/x", "https://u:p@cloudcube.wuxi.cn/x", "https://cloudcube.wuxi.cn/x#fragment", "https://cloudcube.wuxi.cn/x?access_token=x", "https://cloudcube.wuxi.cn/x?key=private-fixture"} {
		data["raw_url"] = bad
		if _, e = r.Resolve(context.Background(), hash, 123, true); e == nil {
			t.Error("unsafe link accepted", bad)
		}
	}
	data["raw_url"] = "https://cloudcube.wuxi.cn/x"
	data["size"] = 124
	if _, e = r.Resolve(context.Background(), hash, 123, true); e == nil {
		t.Fatal("provider size mismatch accepted")
	}
	for _, endpoint := range []string{"http://example.test", "http://127.0.0.1:12/api", "http://u:p@127.0.0.1:12", "http://127.0.0.1:12?x=1"} {
		cfg.Endpoint = endpoint
		if _, e = New(cfg); e == nil {
			t.Error("noncanonical endpoint accepted", endpoint)
		}
	}
}
