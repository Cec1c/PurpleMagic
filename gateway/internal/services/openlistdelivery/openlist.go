// Package openlistdelivery exchanges registered content hashes for short-lived
// Tianyi links. OpenList owns provider credentials and session renewal.
package openlistdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

const CapabilityHeader = "X-DNF90-Cloud-Transport"
const Capability = "tianyi-v1"

type Config struct {
	Endpoint      string `json:"endpoint"`
	TokenFile     string `json:"token_file"`
	ArtifactsFile string `json:"artifacts_file"`
}
type Artifact struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
type cached struct {
	url   string
	until time.Time
}
type Resolver struct {
	cfg       Config
	token     string
	artifacts map[string]Artifact
	client    *http.Client
	mu        sync.Mutex
	cache     map[string]cached
	slots     chan struct{}
}

var unavailable = errors.New("cloud delivery unavailable")
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func readFile(name string, limit int64) ([]byte, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, unavailable
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, unavailable
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, unavailable
	}
	return b, nil
}
func New(c Config) (*Resolver, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, unavailable
	}
	token, e := readFile(c.TokenFile, 16384)
	if e != nil {
		return nil, e
	}
	t := strings.TrimSpace(string(token))
	if t == "" || strings.ContainsAny(t, "\r\n") {
		return nil, unavailable
	}
	b, e := readFile(c.ArtifactsFile, 4<<20)
	if e != nil {
		return nil, e
	}
	r := &Resolver{cfg: c, token: t, cache: map[string]cached{}, slots: make(chan struct{}, 4), client: &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if json.Unmarshal(b, &r.artifacts) != nil || len(r.artifacts) == 0 || len(r.artifacts) > 2000 {
		return nil, unavailable
	}
	for h, a := range r.artifacts {
		if !hashPattern.MatchString(h) || a.Size <= 0 || a.Size > 8<<30 || !strings.HasPrefix(a.Path, "/") || path.Clean(a.Path) != a.Path || strings.ContainsAny(a.Path, "\x00\r\n\\") || !strings.HasPrefix(path.Base(a.Path), h+".") {
			return nil, unavailable
		}
	}
	return r, nil
}
func provider(raw, token string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || u.Hostname() != "cloudcube.wuxi.cn" || strings.Contains(raw, token) {
		return false
	}
	for k := range u.Query() {
		switch strings.ToLower(k) {
		case "access_token", "refreshtoken", "refresh_token", "accesstoken", "sessionkey", "sessionsecret":
			return false
		}
	}
	return true
}
func (r *Resolver) Resolve(ctx context.Context, hash string, size int64, fresh bool) (string, error) {
	a, ok := r.artifacts[hash]
	if !ok || a.Size != size {
		return "", unavailable
	}
	r.mu.Lock()
	c, ok := r.cache[hash]
	if fresh {
		delete(r.cache, hash)
	}
	r.mu.Unlock()
	if ok && !fresh && time.Now().Before(c.until) {
		return c.url, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return "", unavailable
	}
	b, _ := json.Marshal(map[string]any{"path": a.Path, "password": ""})
	req, e := http.NewRequestWithContext(ctx, "POST", r.cfg.Endpoint+"/api/fs/get", bytes.NewReader(b))
	if e != nil {
		return "", unavailable
	}
	req.Header.Set("Authorization", r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := r.client.Do(req)
	if e != nil {
		return "", unavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", unavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return "", unavailable
	}
	var v struct {
		Code int `json:"code"`
		Data struct {
			Size  int64  `json:"size"`
			Name  string `json:"name"`
			URL   string `json:"raw_url"`
			IsDir bool   `json:"is_dir"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Code != 200 || v.Data.IsDir || v.Data.Size != size || v.Data.Name != path.Base(a.Path) || !provider(v.Data.URL, r.token) {
		return "", unavailable
	}
	r.mu.Lock()
	r.cache[hash] = cached{v.Data.URL, time.Now().Add(2 * time.Minute)}
	r.mu.Unlock()
	return v.Data.URL, nil
}

// Old clients retain their existing byte/Baidu behavior, even when Tianyi is enabled.
func (r *Resolver) Redirect(w http.ResponseWriter, req *http.Request, hash string, size int64) bool {
	w.Header().Add("Vary", CapabilityHeader)
	if r == nil || req.Method != "GET" || req.Header.Get(CapabilityHeader) != Capability {
		return false
	}
	link, e := r.Resolve(req.Context(), hash, size, req.Header.Get("X-DNF90-Refresh-Link") == "1")
	if e != nil {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Location", link)
	w.WriteHeader(http.StatusTemporaryRedirect)
	return true
}
