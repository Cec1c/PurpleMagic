package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/quest"
	"dnf90.local/gateway/internal/modules/dnf/skill"
	"dnf90.local/gateway/internal/servicehttp"
)

func TestRemoteCatalogCachesSuccessfulGeneration(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	hash := strings.Repeat("a", 64)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request catalogRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		calls[request.Operation]++
		count := calls[request.Operation]
		mu.Unlock()
		if request.Operation == "metadata" && count == 1 {
			http.Error(w, "temporary", 503)
			return
		}
		var value any
		switch request.Operation {
		case "metadata":
			value = map[string]string{"ManifestHash": hash}
		case "profession":
			value = catalogProfession{ManifestHash: hash, Job: professionJobPayload{Job: request.Job, Profile: professionIndexFixture("cache").readonlyTestSource["character/zero.chr"], Skills: []skill.Skill{{Job: request.Job, ID: 1, Path: "skill/zero/a.skl"}}}, Quests: []quest.Definition{{ID: 1, Path: "n_quest/job.qst"}}}
		case "items":
			rows := []catalogItem{}
			for _, id := range request.ItemIDs {
				rows = append(rows, catalogItem{Entry: itemEntry{ID: id}, Found: true})
			}
			value = rows
		}
		raw, _ := json.Marshal(value)
		_ = json.NewEncoder(w).Encode(catalogResult{Value: raw})
	})
	server := httptest.NewServer(servicehttp.Protect(strings.Repeat("x", 32), hash, handler))
	defer server.Close()
	catalog, err := NewRemoteCatalog(servicehttp.Config{URL: server.URL, Token: strings.Repeat("x", 32), PVFSHA256: hash})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = catalog.metadata(ctx); err == nil {
		t.Fatal("failed metadata was accepted")
	}
	for range 3 {
		if got, err := catalog.metadata(ctx); err != nil || got != hash {
			t.Fatal(got, err)
		}
	}
	for range 3 {
		if _, _, _, err = catalog.profession(ctx, 0); err != nil {
			t.Fatal(err)
		}
	}
	for id := uint32(1); id <= 512; id++ {
		catalog.items[id] = catalogItem{Entry: itemEntry{ID: id}, Found: true}
	}
	if err = catalog.preload(ctx, []uint32{513, 514}); err != nil {
		t.Fatal(err)
	}
	if len(catalog.items) != 512 {
		t.Fatal("cache was flushed", len(catalog.items))
	}
	for _, id := range []uint32{513, 514} {
		if _, found, err := catalog.item(id); err != nil || !found {
			t.Fatal(id, found, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["metadata"] != 2 || calls["profession"] != 1 || calls["items"] != 1 || calls["item"] != 0 {
		t.Fatal(calls)
	}
}
