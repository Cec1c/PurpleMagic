package launcherhub

import (
	"encoding/json"
	"net/http"
)

func newsRevision(news []News) string {
	if news == nil {
		news = []News{}
	}
	raw, _ := json.Marshal(news)
	return digest(raw)
}

// The existing whole-list endpoint also accepts a content revision. Keeping the
// revision specific to news allows independent signed client releases to proceed.
func (h *Hub) publishNews(w http.ResponseWriter, r *http.Request) {
	var news []News
	if err := readBody(w, r, &news); err != nil || news == nil {
		failure(w, 400, "公告请求无效，完整列表 JSON 不能超过 128 KiB；请使用逐条公告管理")
		return
	}
	for _, n := range news {
		if err := validateNews(n); err != nil {
			failure(w, 400, err.Error())
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Older editors only read the launcher window. Do not let a whole-window
	// replacement erase the history they have never seen.
	if len(h.newsEntries()) > len(h.state.Published.News) {
		failure(w, 409, "已有历史公告，请使用新版公告管理逐条编辑或删除")
		return
	}
	before, next := newsRevision(h.state.Published.News), newsRevision(news)
	if before == next {
		reply(w, 200, map[string]any{"ok": true, "newsRevision": before})
		return
	}
	if expected := r.Header.Get("If-Match"); expected != "" && expected != `"`+newsRevision(h.state.Published.News)+`"` && expected != `"`+h.archiveRevision()+`"` {
		failure(w, 409, "公告已被其他窗口修改，请重新载入后编辑")
		return
	}
	previous := h.state
	h.state.NewsArchive = entriesFor(news)
	h.state.NewsArchiveReady = true
	h.state.Published.News = launcherNews(h.state.NewsArchive)
	h.state.Published.Sequence++
	if err := h.save(); err != nil {
		h.state = previous
		failure(w, 500, "发布公告失败")
		return
	}
	reply(w, 200, map[string]any{"ok": true, "newsRevision": h.archiveRevision()})
}
