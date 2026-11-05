package launcherhub

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetadataTransportPreservesEnvelope(t *testing.T) {
	raw := []byte(strings.Repeat(`{"Payload":"signed immutable payload","Signature":"fixture"}`, 100))
	for _, enc := range []string{"", "gzip", "gzip;q=0", "gzip;q=0.5", "deflate, gzip", "gzip;q=bad"} {
		req := httptest.NewRequest("GET", "https://gateway.test/launcher/v2/bootstrap.json", nil)
		req.Header.Set("Accept-Encoding", enc)
		w := httptest.NewRecorder()
		writeBootstrap(w, req, raw)
		expected := enc == "gzip" || enc == "gzip;q=0.5" || enc == "deflate, gzip"
		if (w.Header().Get("Content-Encoding") == "gzip") != expected {
			t.Fatal("encoding negotiation", enc)
		}
		got := w.Body.Bytes()
		if expected {
			z, e := gzip.NewReader(w.Body)
			if e != nil {
				t.Fatal(e)
			}
			got, e = io.ReadAll(z)
			z.Close()
			if e != nil {
				t.Fatal(e)
			}
		}
		if !bytes.Equal(got, raw) {
			t.Fatal("signed envelope bytes changed")
		}
		req.Method = "HEAD"
		w = httptest.NewRecorder()
		writeBootstrap(w, req, raw)
		if w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
			t.Fatal("HEAD contract")
		}
	}
}
