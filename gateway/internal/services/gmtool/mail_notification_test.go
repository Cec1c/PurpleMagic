package gmtool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

type mailboxNotificationRequest struct {
	Action      string `json:"action"`
	CharacterID uint16 `json:"characterId"`
}

func mailboxNotificationServer(t *testing.T, handler func(mailboxNotificationRequest, http.ResponseWriter, *http.Request)) *adminapi.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != adminapi.Path {
			http.NotFound(w, r)
			return
		}
		var request mailboxNotificationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode game admin request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		handler(request, w, r)
	}))
	t.Cleanup(server.Close)
	client, err := adminapi.NewClient(server.URL+adminapi.Path, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func mailboxSuccessResponse(w http.ResponseWriter, online bool) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(adminapi.Response{Version: adminapi.Version, OK: true, Online: online})
}

func TestNotifyMailboxReceiptsUsesReceiptIdentityAndOfflineIsSuccess(t *testing.T) {
	var mu sync.Mutex
	var requests []mailboxNotificationRequest
	client := mailboxNotificationServer(t, func(request mailboxNotificationRequest, w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		mailboxSuccessResponse(w, false)
	})
	var logs []string
	s := &Server{deps: Deps{Runtime: client}, cfg: Config{Logger: func(kind string, _ ...any) {
		mu.Lock()
		logs = append(logs, kind)
		mu.Unlock()
	}}}
	err := s.notifyMailboxReceipts(context.Background(), []testbenefits.DeliveryReceipt{
		{MailID: "mail-1", CharacterID: "88"},
		{MailID: "duplicate", CharacterID: "88"},
		{CharacterID: "77"}, // synthetic receipt without an inbox row
		{MailID: "mail-2", CharacterID: "89"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0].Action != notifyMailboxAction || requests[0].CharacterID != 88 || requests[1].CharacterID != 89 {
		t.Fatalf("notification identity/dedup = %+v", requests)
	}
	if len(logs) != 2 || logs[0] != "gm-mailbox-notification" || logs[1] != "gm-mailbox-notification" {
		t.Fatalf("offline result must be a successful diagnostic log: %v", logs)
	}
}

func TestNotifyMailboxReceiptsFailureIsDiagnosticAndBounded(t *testing.T) {
	reached := make(chan struct{}, 2)
	client := mailboxNotificationServer(t, func(_ mailboxNotificationRequest, w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		<-r.Context().Done()
	})
	var mu sync.Mutex
	var logs []string
	s := &Server{deps: Deps{Runtime: client}, cfg: Config{Logger: func(kind string, _ ...any) {
		mu.Lock()
		logs = append(logs, kind)
		mu.Unlock()
	}}}
	started := time.Now()
	err := s.notifyMailboxReceiptsWithin(context.Background(), []testbenefits.DeliveryReceipt{
		{MailID: "mail-1", CharacterID: "88"},
		{MailID: "mail-2", CharacterID: "89"},
	}, 40*time.Millisecond)
	if err == nil {
		t.Fatal("bounded transport failure must remain diagnosable")
	}
	select {
	case <-reached:
	default:
		t.Fatal("timeout test did not reach the HTTP server")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("notification budget not bounded: %s", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 || logs[0] != "gm-mailbox-notification-failed" {
		t.Fatalf("missing pending diagnostic: %v", logs)
	}
}

func TestSingleItemMailNotificationAfterCommitAndOnReceiptReplay(t *testing.T) {
	harness := newGmtoolWriteHarness(t, nil)
	ctx := context.Background()
	if err := harness.group.Account.Save(ctx, repository.AccountRecord{AccountID: "acc-1", Metadata: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	index := newItemIndex("", harness.server, harness.server.cfg.Logger)
	index.setEntriesLocked([]itemEntry{{ID: 910001, Name: "通知测试物品", Kind: "stackable"}})
	harness.server.items = index

	var calls, mailCountAtCall int
	client := mailboxNotificationServer(t, func(request mailboxNotificationRequest, w http.ResponseWriter, _ *http.Request) {
		calls++
		if request.Action != notifyMailboxAction || request.CharacterID != 1 {
			t.Errorf("unexpected notification request: %+v", request)
		}
		box, _, err := harness.group.Mailbox.Load(context.Background(), "1")
		if err != nil || len(box.Mails) != 1 {
			t.Errorf("notification before durable mail commit: mails=%d err=%v", len(box.Mails), err)
		}
		mailCountAtCall = len(box.Mails)
		mailboxSuccessResponse(w, true)
	})
	harness.server.deps.Runtime = client
	body := map[string]any{"templateId": 910001, "count": 1, "requestId": "notify-request-0001"}
	post := func() map[string]any {
		t.Helper()
		status, response := harness.post(t, "/api/characters/1/items", map[string]string{"id": "1"}, body, harness.server.handleItemGrant)
		if status != http.StatusOK || response["success"] != true {
			t.Fatalf("mail grant failed: %d %+v", status, response)
		}
		return response
	}
	first := post()
	second := post()
	if calls != 2 || mailCountAtCall != 1 {
		t.Fatalf("notification calls=%d mails_at_call=%d", calls, mailCountAtCall)
	}
	if first["messageId"] == "" || first["messageId"] != second["messageId"] || second["replayed"] != true {
		t.Fatalf("receipt replay changed mail identity: first=%+v second=%+v", first, second)
	}
	box, _, err := harness.group.Mailbox.Load(ctx, "1")
	if err != nil || len(box.Mails) != 1 {
		t.Fatalf("receipt replay rewrote mail: mails=%d err=%v", len(box.Mails), err)
	}
}

func TestSingleItemMailNotificationFailureDoesNotFailCommittedGrant(t *testing.T) {
	harness := newGmtoolWriteHarness(t, nil)
	ctx := context.Background()
	if err := harness.group.Account.Save(ctx, repository.AccountRecord{AccountID: "acc-1", Metadata: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	index := newItemIndex("", harness.server, harness.server.cfg.Logger)
	index.setEntriesLocked([]itemEntry{{ID: 910002, Name: "失败通知测试物品", Kind: "stackable"}})
	harness.server.items = index
	reached := make(chan struct{}, 1)
	var logs []string
	client := mailboxNotificationServer(t, func(_ mailboxNotificationRequest, w http.ResponseWriter, _ *http.Request) {
		reached <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(adminapi.Response{Version: adminapi.Version, OK: false, Error: "fixture failure"})
	})
	harness.server.deps.Runtime = client
	harness.server.cfg.Logger = func(kind string, _ ...any) { logs = append(logs, kind) }
	status, response := harness.post(t, "/api/characters/1/items", map[string]string{"id": "1"}, map[string]any{
		"templateId": 910002, "count": 1, "requestId": "notify-failure-0001",
	}, harness.server.handleItemGrant)
	select {
	case <-reached:
	default:
		t.Fatal("application failure test did not reach the HTTP server")
	}
	if status != http.StatusOK || response["success"] != true {
		t.Fatalf("committed mail was misreported as grant failure: %d %+v", status, response)
	}
	box, _, err := harness.group.Mailbox.Load(ctx, "1")
	if err != nil || len(box.Mails) != 1 {
		t.Fatalf("notification failure changed committed mail: mails=%d err=%v", len(box.Mails), err)
	}
	if len(logs) == 0 || logs[len(logs)-1] != "gm-mailbox-notification-failed" {
		t.Fatalf("missing notification diagnostic: %v", logs)
	}
}

func TestNotifyMailboxReceiptsInvalidIdentityIsDiagnostic(t *testing.T) {
	client := mailboxNotificationServer(t, func(_ mailboxNotificationRequest, _ http.ResponseWriter, _ *http.Request) {
		t.Error("invalid identity reached the game API")
	})
	s := &Server{deps: Deps{Runtime: client}, cfg: Config{Logger: func(kind string, _ ...any) {
		if kind != "gm-mailbox-notification-failed" {
			t.Errorf("unexpected log kind %q", kind)
		}
	}}}
	err := s.notifyMailboxReceipts(context.Background(), []testbenefits.DeliveryReceipt{{MailID: "mail", CharacterID: "bad"}})
	if err == nil || !strings.Contains(err.Error(), "invalid receipt character id") {
		t.Fatalf("invalid receipt identity error = %v", err)
	}
}
