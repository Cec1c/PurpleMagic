package launcherhub

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var contentBlobPath = regexp.MustCompile(`^/blobs/([a-f0-9]{64})$`)

// Only hashes named by the current signed, locally hosted publication are public.
// Operators stage immutable verified blobs before advancing the publication.
func (h *Hub) serveContentBlob(w http.ResponseWriter, r *http.Request) bool {
	m := contentBlobPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return false
	}
	var size int64
	h.mu.Lock()
	c := h.state.Published.Content
	if c != nil && c.Endpoint == strings.TrimRight(h.profile.ApiURL, "/") {
		for _, f := range c.Files {
			key, length := f.Sha256, f.Length
			if f.Compression == "gzip" {
				key, length = f.BlobSha256, f.BlobLength
			}
			if f.Downloadable && key == m[1] {
				size = length
				break
			}
		}
	}
	h.mu.Unlock()
	if size <= 0 {
		http.NotFound(w, r)
		return true
	}
	root, err := os.OpenRoot(filepath.Join(h.cfg.DataDir, "content-blobs"))
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	defer root.Close()
	info, err := root.Lstat(m[1])
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		http.NotFound(w, r)
		return true
	}
	f, err := root.Open(m[1])
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		http.NotFound(w, r)
		return true
	}
	if h.openlist.Redirect(w, r, m[1], size) || h.baidu.Redirect(w, r, m[1], size) {
		return true
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+m[1]+`"`)
	http.ServeContent(w, r, m[1], time.Time{}, f)
	return true
}
