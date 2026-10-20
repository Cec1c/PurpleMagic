package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/servicehttp"
)

func TestExternalScannerResumesWithinAccount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var seen []benefitScanPosition
	next := benefitScanPosition{Account: 5, Character: "100", Campaign: strings.Repeat("a", 32)}
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/gateway/v1/scan-v2" {
			t.Errorf("unexpected scan route: %s", r.URL.Path)
		}
		var in benefitScanPosition
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		mu.Lock()
		seen = append(seen, in)
		count := len(seen)
		mu.Unlock()
		w.Header().Set("X-PurpleMagic-Protocol", servicehttp.Version)
		w.Header().Set("X-PVF-SHA256", strings.Repeat("b", 64))
		if count == 1 {
			json.NewEncoder(w).Encode(map[string]any{"position": next, "retry": false})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"position": benefitScanPosition{}, "retry": false})
			cancel()
		}
	}))
	defer peerServer.Close()
	peer, err := servicehttp.New(servicehttp.Config{URL: peerServer.URL, Token: strings.Repeat("t", 48), PVFSHA256: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err = RunExternalScanner(ctx, peer, 5*time.Second, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || !reflect.DeepEqual(seen[1], next) {
		t.Fatal("lost within-account resume position", seen)
	}
}
