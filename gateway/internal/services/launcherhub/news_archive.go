package launcherhub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The archive is management data. Only a bounded window enters the signed
// bootstrap, so existing launchers continue to accept it as history grows.
type NewsEntry struct {
	ID string `json:"id"`
	News
}

type newsOperation struct {
	Digest   string `json:"digest"`
	Revision string `json:"revision"`
	ID       string `json:"id"`
}

func validateNews(n News) error {
	if strings.TrimSpace(n.Title) == "" {
		return errors.New("请填写公告标题")
	}
	if utf8.RuneCountInString(n.Title) > 160 {
		return errors.New("公告标题最多 160 字")
	}
	if len(n.Body) > 64<<10 {
		return errors.New("单篇公告正文最多 64 KiB")
	}
	if len(n.Date) > 32 {
		return errors.New("公告日期最多 32 字节")
	}
	if !utf8.ValidString(n.Title+n.Body+n.Date) || strings.ContainsRune(n.Title+n.Body+n.Date, 0) {
		return errors.New("公告包含无效字符")
	}
	if n.Category != "综合" && n.Category != "更新" && n.Category != "指南" {
		return errors.New("公告分类无效")
	}
	if n.Format != "" && n.Format != "text" && n.Format != "markdown" {
		return errors.New("公告正文格式无效")
	}
	return nil
}

func entriesFor(news []News) []NewsEntry {
	entries := make([]NewsEntry, 0, len(news))
	for i, n := range news {
		raw, _ := json.Marshal(n)
		entries = append(entries, NewsEntry{ID: digest(append([]byte(strconv.Itoa(i)+":"), raw...))[:32], News: n})
	}
	return entries
}

// Migration is lazy: simply starting a new binary never changes signed bytes.
// Caller holds h.mu. Every mutation allocates a new slice before saving.
func (h *Hub) newsEntries() []NewsEntry {
	if h.state.NewsArchiveReady {
		return h.state.NewsArchive
	}
	return entriesFor(h.state.Published.News)
}

func (h *Hub) archiveRevision() string {
	if !h.state.NewsArchiveReady {
		return newsRevision(h.state.Published.News)
	}
	raw, _ := json.Marshal(h.state.NewsArchive)
	return digest(raw)
}

func launcherNews(entries []NewsEntry) []News {
	news := make([]News, 0, min(30, len(entries)))
	bytes := 2
	for _, entry := range entries {
		raw, _ := json.Marshal(entry.News)
		if len(news) == 30 || bytes+len(raw)+1 > 512<<10 {
			break
		}
		news = append(news, entry.News)
		bytes += len(raw) + 1
	}
	return news
}

func (h *Hub) listNews(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, e := strconv.Atoi(q.Get("offset"))
	if q.Get("offset") == "" {
		offset, e = 0, nil
	}
	limit, le := strconv.Atoi(q.Get("limit"))
	if q.Get("limit") == "" {
		limit, le = 20, nil
	}
	if e != nil || le != nil || offset < 0 || limit < 1 || limit > 50 || len(q.Get("q")) > 512 {
		failure(w, 400, "公告分页参数无效")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	revision := h.archiveRevision()
	if expected := q.Get("revision"); expected != "" && expected != revision {
		failure(w, 409, "公告列表已更新，请刷新列表")
		return
	}
	entries := h.newsEntries()
	matched := make([]NewsEntry, 0)
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	for _, entry := range entries {
		if search == "" || strings.Contains(strings.ToLower(entry.Title+"\n"+entry.Body), search) {
			matched = append(matched, entry)
		}
	}
	end := min(offset+limit, len(matched))
	start := min(offset, end)
	next := -1
	if end < len(matched) {
		next = end
	}
	reply(w, 200, map[string]any{"rows": matched[start:end], "total": len(matched), "archiveTotal": len(entries), "offset": start, "next": next, "revision": revision, "launcherCount": len(h.state.Published.News)})
}

type newsMutation struct {
	Action    string `json:"action"`
	ID        string `json:"id"`
	News      *News  `json:"news,omitempty"`
	Direction string `json:"direction,omitempty"`
}

func (h *Hub) mutateNews(w http.ResponseWriter, r *http.Request) {
	var in newsMutation
	if err := readBody(w, r, &in); err != nil {
		failure(w, 400, "公告请求无效或超过 128 KiB")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 100 || strings.ContainsAny(key, "\r\n\x00") {
		failure(w, 400, "缺少有效的操作编号")
		return
	}
	if in.Action != "save" && in.Action != "delete" && in.Action != "move" {
		failure(w, 400, "公告操作无效")
		return
	}
	if in.Action == "save" {
		if in.News == nil {
			failure(w, 400, "缺少公告内容")
			return
		}
		if err := validateNews(*in.News); err != nil {
			failure(w, 400, err.Error())
			return
		}
	} else if in.News != nil {
		failure(w, 400, "该操作不接受公告正文")
		return
	}
	if in.Action == "move" && in.Direction != "up" && in.Direction != "down" {
		failure(w, 400, "移动方向无效")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	raw, _ := json.Marshal(in)
	hash := digest(raw)
	if prior, ok := h.state.NewsOperations[key]; ok {
		if prior.Digest != hash {
			failure(w, 409, "操作编号已用于其他公告修改")
			return
		}
		reply(w, 200, map[string]any{"ok": true, "id": prior.ID, "newsRevision": prior.Revision})
		return
	}
	if r.Header.Get("If-Match") != fmt.Sprintf("%q", h.archiveRevision()) {
		failure(w, 409, "公告列表已更新，请刷新后重新确认")
		return
	}
	entries := slices.Clone(h.newsEntries())
	// Drafts saved by the former UI used the published array index. Resolve
	// those only against an unmigrated archive and its exact revision.
	if !h.state.NewsArchiveReady && in.ID != "" {
		if oldIndex, err := strconv.Atoi(in.ID); err == nil && oldIndex >= 0 && oldIndex < len(entries) {
			in.ID = entries[oldIndex].ID
		}
	}
	index := slices.IndexFunc(entries, func(n NewsEntry) bool { return n.ID == in.ID })
	if (in.ID != "" || in.Action != "save") && index < 0 {
		failure(w, 404, "公告已删除，请刷新列表")
		return
	}
	id := in.ID
	switch in.Action {
	case "save":
		if index < 0 {
			id = digest([]byte("news:" + key))[:32]
			entries = append([]NewsEntry{{ID: id, News: *in.News}}, entries...)
		} else {
			entries[index].News = *in.News
		}
	case "delete":
		entries = slices.Delete(entries, index, index+1)
	case "move":
		other := index - 1
		if in.Direction == "down" {
			other = index + 1
		}
		if other >= 0 && other < len(entries) {
			entries[index], entries[other] = entries[other], entries[index]
		}
	}
	if entries == nil {
		entries = []NewsEntry{}
	}
	previous := h.state
	h.state.NewsArchive = entries
	h.state.NewsArchiveReady = true
	projected := launcherNews(entries)
	if newsRevision(projected) != newsRevision(previous.Published.News) {
		h.state.Published.News = projected
		h.state.Published.Sequence++
	}
	ops := make(map[string]newsOperation)
	// Evicted requests still carry their old If-Match and cannot execute again.
	if len(previous.NewsOperations) < 128 {
		for k, v := range previous.NewsOperations {
			ops[k] = v
		}
	}
	revision := h.archiveRevision()
	ops[key] = newsOperation{Digest: hash, Revision: revision, ID: id}
	h.state.NewsOperations = ops
	if err := h.save(); err != nil {
		h.state = previous
		failure(w, 500, "保存公告失败，请用原操作重试")
		return
	}
	reply(w, 200, map[string]any{"ok": true, "id": id, "newsRevision": revision})
}
