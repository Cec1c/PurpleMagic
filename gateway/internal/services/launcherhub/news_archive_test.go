package launcherhub

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newsClient(t *testing.T, h *Hub) func(string, string, any, string, string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, csrf := auth(t, h, false)
	return func(method, path string, body any, revision, key string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://admin.example.test"+path, strings.NewReader(string(raw)))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", csrf)
		if revision != "" {
			r.Header.Set("If-Match", fmt.Sprintf("%q", revision))
		}
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.AdminHandler().ServeHTTP(w, r)
		return w
	}
}

func TestNewsArchiveGrowthDeleteAndReplay(t *testing.T) {
	h, _ := fixture(t)
	client := newsClient(t, h)
	old := make([]News, 30)
	for i := range old {
		old[i] = News{Title: fmt.Sprintf("旧公告%d", i), Category: "综合", Body: "保留\n文本"}
	}
	h.state.Published.News = old
	if err := h.save(); err != nil {
		t.Fatal(err)
	}
	sequence := h.state.Published.Sequence
	ids := h.newsEntries()
	if w := client("GET", "/admin/api/news?limit=20", nil, "", ""); w.Code != 200 || h.state.Published.Sequence != sequence || h.state.NewsArchiveReady {
		t.Fatal("read mutated published state", w.Code)
	}
	var firstKey, firstRevision, firstID string
	for i := 0; i < 75; i++ {
		rev := h.archiveRevision()
		key := fmt.Sprintf("create-%d", i)
		body := newsMutation{Action: "save", News: &News{Title: fmt.Sprintf("新公告%d", i), Category: "更新", Format: "markdown", Body: "## 标题\n\n**更新**\n\n| 项目 | 结果 |\n| --- | --- |\n| 公告 | 完成 |"}}
		w := client("POST", "/admin/api/news/entries", body, rev, key)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if i == 0 {
			firstKey, firstRevision, firstID = key, rev, h.state.NewsArchive[0].ID
		}
	}
	if len(h.state.NewsArchive) != 105 || len(h.state.Published.News) != 30 || h.state.Published.News[0].Title != "新公告74" {
		t.Fatal("archive or bounded launcher window wrong")
	}
	if h.state.NewsArchive[75].ID != ids[0].ID {
		t.Fatal("migration lost stable IDs")
	}
	w := client("GET", "/admin/api/news?offset=100&limit=20", nil, "", "")
	var page struct {
		Rows  []NewsEntry
		Total int
		Next  int
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || page.Total != 105 || len(page.Rows) != 5 || page.Next != -1 {
		t.Fatal("pagination", w.Body.String())
	}
	remove := newsMutation{Action: "delete", ID: ids[27].ID}
	rev := h.archiveRevision()
	seq := h.state.Published.Sequence
	w = client("POST", "/admin/api/news/entries", remove, rev, "delete-old")
	if w.Code != 200 || len(h.state.NewsArchive) != 104 || h.state.Published.Sequence != seq {
		t.Fatal("old deletion affected launcher or failed", w.Body.String())
	}
	w = client("POST", "/admin/api/news/entries", remove, rev, "delete-old")
	if w.Code != 200 || len(h.state.NewsArchive) != 104 {
		t.Fatal("delete replay failed")
	}
	w = client("POST", "/admin/api/news/entries", newsMutation{Action: "delete", ID: firstID}, rev, "stale-delete")
	if w.Code != 409 {
		t.Fatal("stale window accepted")
	}
	// Retry the original create after later writes; no duplicate is added.
	create := newsMutation{Action: "save", News: &News{Title: "新公告0", Category: "更新", Format: "markdown", Body: "## 标题\n\n**更新**\n\n| 项目 | 结果 |\n| --- | --- |\n| 公告 | 完成 |"}}
	w = client("POST", "/admin/api/news/entries", create, firstRevision, firstKey)
	if w.Code != 200 || len(h.state.NewsArchive) != 104 {
		t.Fatal("lost-response replay duplicated article")
	}
	w = client("PUT", "/admin/api/news", h.state.Published.News, newsRevision(h.state.Published.News), "")
	if w.Code != 409 || len(h.state.NewsArchive) != 104 {
		t.Fatal("legacy writer erased unseen history")
	}
	restarted, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.archiveRevision() != h.archiveRevision() || len(restarted.newsEntries()) != 104 || restarted.state.Published.News[0].Format != "markdown" {
		t.Fatal("archive/Markdown lost after restart")
	}
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		h.PublicHandler().ServeHTTP(w, httptest.NewRequest(method, "/admin/api/news/entries", nil))
		if w.Code != 404 {
			t.Fatal("archive exposed on player listener")
		}
	}
}

func TestNewsArchiveValidationAndAtomicSave(t *testing.T) {
	h, _ := fixture(t)
	client := newsClient(t, h)
	before := h.archiveRevision()
	sequence := h.state.Published.Sequence
	article := News{Title: "文章", Category: "指南", Format: "markdown", Body: strings.Repeat("中", 21000)}
	w := client("POST", "/admin/api/news/entries", newsMutation{Action: "save", News: &article}, before, "large-body")
	if w.Code != 200 {
		t.Fatal("long Chinese Markdown rejected", w.Code, w.Body.String())
	}
	id := h.state.NewsArchive[0].ID
	rev := h.archiveRevision()
	for _, n := range []News{{Title: "", Category: "综合"}, {Title: "x", Category: "其他"}, {Title: "x", Category: "综合", Format: "html"}, {Title: "x", Category: "综合", Body: strings.Repeat("x", 65537)}} {
		w = client("POST", "/admin/api/news/entries", newsMutation{Action: "save", News: &n}, rev, "invalid")
		if w.Code != 400 || h.archiveRevision() != rev {
			t.Fatal("invalid mutation changed archive")
		}
	}
	article.Body = "changed"
	w = client("POST", "/admin/api/news/entries", newsMutation{Action: "save", News: &article}, rev, "large-body")
	if w.Code != 409 {
		t.Fatal("key reuse accepted")
	}
	// Atomic persistence failure keeps both the archive and signed window.
	oldDir := h.cfg.DataDir
	bad := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(bad, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	h.cfg.DataDir = bad
	w = client("POST", "/admin/api/news/entries", newsMutation{Action: "delete", ID: id}, rev, "delete-failure")
	if w.Code != 500 || h.archiveRevision() != rev || h.state.Published.Sequence != sequence+1 {
		t.Fatal("failed persistence changed state")
	}
	h.cfg.DataDir = oldDir
	w = client("POST", "/admin/api/news/entries", newsMutation{Action: "delete", ID: id}, rev, "delete-failure")
	if w.Code != 200 || len(h.newsEntries()) != 0 || len(h.state.Published.News) != 0 {
		t.Fatal("delete to empty/retry failed")
	}
}
