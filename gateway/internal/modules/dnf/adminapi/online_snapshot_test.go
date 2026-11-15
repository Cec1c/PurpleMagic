package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOnlineSnapshotClientBoundsAndCompleteness(t *testing.T) {
	valid := &OnlineSnapshot{ID: strings.Repeat("a", 32), ObservedAt: time.Now().UTC(), Complete: true, Members: []OnlineMember{}}
	for i := 1; i <= MaxOnlineSnapshotMembers; i++ {
		valid.Members = append(valid.Members, OnlineMember{CharacterID: uint16(i), Generation: ^uint64(0)})
	}
	response := Response{Version: Version, OK: true, OnlineSnapshot: valid}
	raw, _ := json.Marshal(response)
	if len(raw) <= 65536 {
		t.Fatal("fixture below old body limit")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	client, _ := NewClient(server.URL+Path, strings.Repeat("x", 32))
	snapshot, err := client.OnlineSnapshot(context.Background())
	if err != nil || len(snapshot.Members) != MaxOnlineSnapshotMembers {
		t.Fatal(err)
	}
	if _, err = client.Call(context.Background(), Request{Action: "online", CharacterID: 1}); err != nil {
		t.Fatal("unified 4 MiB response limit rejected >64 KiB response", err)
	}
	for _, bad := range []*OnlineSnapshot{nil, {}, {ID: strings.Repeat("a", 32), ObservedAt: time.Now(), Complete: true}, {ID: strings.Repeat("a", 32), ObservedAt: time.Now(), Complete: true, Members: []OnlineMember{{CharacterID: 1, Generation: 2}, {CharacterID: 1, Generation: 3}}}} {
		response.OnlineSnapshot = bad
		raw, _ = json.Marshal(response)
		if _, err = client.OnlineSnapshot(context.Background()); err == nil {
			t.Fatal("incomplete snapshot accepted")
		}
	}
	raw = []byte(strings.Repeat(" ", 4<<20) + "{}")
	if _, err = client.OnlineSnapshot(context.Background()); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
	var absent *Client
	if _, err = absent.OnlineSnapshot(context.Background()); err == nil {
		t.Fatal("missing client accepted")
	}
}
