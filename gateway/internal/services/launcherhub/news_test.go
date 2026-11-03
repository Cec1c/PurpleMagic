package launcherhub

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNewsRevisionPublicationAndRecovery(t *testing.T) {
	h, key := fixture(t)
	cookie, csrf := auth(t, h, false)
	write := func(body, revision string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "https://admin.example.test/admin/api/news", strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", csrf)
		if revision != "" {
			r.Header.Set("If-Match", `"`+revision+`"`)
		}
		w := httptest.NewRecorder()
		h.AdminHandler().ServeHTTP(w, r)
		return w
	}
	initial := newsRevision(h.state.Published.News)
	sequence := h.state.Published.Sequence
	news := `[{"title":"更新公告","body":"第一行\n第二行 <b>原文</b>","category":"更新","date":"10 / 15"}]`
	if w := write(news, initial); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current := newsRevision(h.state.Published.News)
	if current == initial || h.state.Published.Sequence != sequence+1 {
		t.Fatal("publication revision not advanced")
	}
	if w := write(news, initial); w.Code != 200 || h.state.Published.Sequence != sequence+1 {
		t.Fatal("response replay advanced publication")
	}
	if w := write(`[]`, initial); w.Code != 409 {
		t.Fatal("stale edit accepted", w.Code)
	}
	invalid := []string{`null`, `[{}]`, `[{"title":" ","category":"综合"}]`, `[{"title":"x","category":"invalid"}]`, `[{"title":"x","category":"综合","unknown":1}]`, `[{"title":"x","category":"综合","body":"\u0000"}]`}
	for _, body := range invalid {
		if w := write(body, current); w.Code != 400 {
			t.Fatal("invalid news accepted", body, w.Code)
		}
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, title := range []string{"窗口甲", "窗口乙"} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			codes <- write(strings.Replace(news, "更新公告", title, 1), current).Code
		}(title)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("lost concurrent edit", counts)
	}
	restarted, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if newsRevision(restarted.state.Published.News) != newsRevision(h.state.Published.News) {
		t.Fatal("news lost after restart")
	}
	w := call(restarted.AdminHandler(), "GET", "/admin/api/state", "", cookie, "", "")
	var state map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state["newsRevision"] != h.archiveRevision() {
		t.Fatal("missing editor revision")
	}
	public := httptest.NewRecorder()
	restarted.PublicHandler().ServeHTTP(public, httptest.NewRequest("GET", "/launcher/v2/bootstrap.json", nil))
	var envelope Envelope
	if err = json.Unmarshal(public.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if err = rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, digest[:], signature, nil); err != nil {
		t.Fatal("news signature invalid", err)
	}
	var published Bootstrap
	if err = json.Unmarshal(payload, &published); err != nil || newsRevision(published.News) != newsRevision(h.state.Published.News) {
		t.Fatal("public bootstrap news mismatch", err)
	}
	for _, method := range []string{"GET", "PUT"} {
		w := httptest.NewRecorder()
		h.PublicHandler().ServeHTTP(w, httptest.NewRequest(method, "/admin/api/news", strings.NewReader(news)))
		if w.Code != http.StatusNotFound {
			t.Fatal("public admin endpoint exposed")
		}
	}
	// Existing clients without If-Match can still publish the same array contract.
	if w := write(`[]`, ""); w.Code != 200 || len(h.state.Published.News) != 0 {
		t.Fatal("legacy endpoint changed")
	}
}
