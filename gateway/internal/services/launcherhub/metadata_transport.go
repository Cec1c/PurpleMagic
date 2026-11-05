package launcherhub

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// Compress only the HTTP representation; the signed envelope is unchanged.
func writeBootstrap(w http.ResponseWriter, r *http.Request, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "Accept-Encoding")
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		quality := 1.0
		for _, field := range fields[1:] {
			k, v, ok := strings.Cut(strings.TrimSpace(field), "=")
			if ok && strings.EqualFold(k, "q") {
				var e error
				quality, e = strconv.ParseFloat(v, 64)
				if e != nil {
					quality = 0
				}
			}
		}
		if quality <= 0 || quality > 1 {
			continue
		}
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		_, e := z.Write(raw)
		closeErr := z.Close()
		if e == nil && closeErr == nil && compressed.Len() < len(raw) {
			raw = compressed.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
		}
		break
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	if r.Method != "HEAD" {
		w.Write(raw)
	}
}
