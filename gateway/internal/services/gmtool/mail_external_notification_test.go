package gmtool

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dnf90.local/gateway/internal/modules/dnf/adminapi"
	dnfpvf "dnf90.local/gateway/internal/modules/dnf/pvf"
	"dnf90.local/gateway/internal/modules/dnf/repository"
	"dnf90.local/gateway/internal/modules/dnf/testbenefits"
)

// Retain the updated job only at Commit, so the second HTTP request observes
// the revision actually written by the first request rather than a canned row.
type externalMailConnector struct{ state *benefitRunSQLState }

func (c externalMailConnector) Driver() driver.Driver { return benefitRunDriver{state: c.state} }
func (c externalMailConnector) Connect(context.Context) (driver.Conn, error) {
	return &externalMailConn{benefitRunConn: benefitRunConn{state: c.state}}, nil
}

type externalMailConn struct {
	benefitRunConn
	pending []byte
}

func (c *externalMailConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *externalMailConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.pending = nil
	return externalMailTx{c}, nil
}
func (c *externalMailConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if !strings.HasPrefix(strings.TrimSpace(query), "UPDATE ") || len(args) != 2 {
		return nil, errors.New("unexpected external mail fixture SQL")
	}
	switch payload := args[0].Value.(type) {
	case []byte:
		c.pending = append([]byte(nil), payload...)
	case string:
		c.pending = []byte(payload)
	default:
		return nil, errors.New("unexpected external mail payload")
	}
	return driver.RowsAffected(1), nil
}

type externalMailTx struct{ conn *externalMailConn }

func (t externalMailTx) Rollback() error { t.conn.pending = nil; return nil }
func (t externalMailTx) Commit() error {
	s := t.conn.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		return s.commitErr
	}
	if t.conn.pending != nil {
		s.payload = append([]byte(nil), t.conn.pending...)
	}
	s.committed = true
	return nil
}

type externalMailNotificationChain struct {
	harness      *gmtoolWriteHarness
	frontend     *Server
	handler      http.Handler
	token        string
	gatewayCalls *atomic.Int64
}

// newExternalMailNotificationChain uses the existing commit-boundary SQL
// fixture only to supply one already-previewed task. Delivery, receipt commit,
// GatewayHandler authentication and Runtime notification all use real paths.
func newExternalMailNotificationChain(t *testing.T, runtime *adminapi.Client, state *benefitRunSQLState, jobID string) *externalMailNotificationChain {
	t.Helper()
	harness := newGmtoolWriteHarness(t, nil)
	harness.server.cfg.Password = strings.Repeat("p", 48)
	source := newItemCacheTestSource(readonlyTestSource{})
	harness.server.deps.PVF = func() (dnfpvf.Source, error) { return source, nil }
	harness.server.pvf = newPVFCache()
	if err := harness.group.Account.Save(context.Background(), repository.AccountRecord{AccountID: "acc-1", Metadata: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	job := testbenefits.Job{
		ID: jobID, Scope: testbenefits.ScopeCharacter, Title: "external mail notification", Gold: 1,
		Recipients: []testbenefits.Recipient{{AccountID: "acc-1", CharacterID: "1", Name: "tester"}},
	}
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	state.payload = payload
	db := sql.OpenDB(externalMailConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	store, err := testbenefits.NewStore(db, "web_accounts")
	if err != nil {
		t.Fatal(err)
	}
	harness.server.deps.Benefits = store
	harness.server.deps.Runtime = runtime

	frontend, peer, gatewayCalls := externalTestServices(t, harness.server)
	if frontend.deps.Benefits != nil || frontend.deps.Runtime != nil {
		t.Fatalf("external catalog acquired gateway dependencies: benefits=%v runtime=%v", frontend.deps.Benefits != nil, frontend.deps.Runtime != nil)
	}
	token, err := frontend.startSession()
	if err != nil {
		t.Fatal(err)
	}
	return &externalMailNotificationChain{
		harness: harness, frontend: frontend, handler: frontend.ExternalHandler("", "", peer),
		token: token, gatewayCalls: gatewayCalls,
	}
}

func (f *externalMailNotificationChain) runTask(t *testing.T, jobID string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+jobID+"/run", strings.NewReader(`{"revision":0,"confirm":true,"retry":false}`))
	request.AddCookie(&http.Cookie{Name: CookieName, Value: f.token})
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", f.frontend.v1CSRF(request))
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, request)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid v1 response: %d %s: %v", w.Code, w.Body.String(), err)
	}
	return w.Code, out
}

func externalMailNotificationData(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	data, ok := response["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing v1 data: %+v", response)
	}
	return data
}

func TestExternalMailV1NotificationIsAfterGatewayCommitAndReplayIsQuiet(t *testing.T) {
	state := &benefitRunSQLState{}
	var runtimeCalls atomic.Int64
	var fixture *externalMailNotificationChain
	client := mailboxNotificationServer(t, func(request mailboxNotificationRequest, w http.ResponseWriter, _ *http.Request) {
		runtimeCalls.Add(1)
		if request.Action != notifyMailboxAction || request.CharacterID != 1 {
			t.Errorf("notification request = %+v", request)
		}
		if !state.wasCommitted() {
			t.Error("Runtime notification ran before the task commit")
		}
		box, _, err := fixture.harness.group.Mailbox.Load(context.Background(), "1")
		if err != nil || len(box.Mails) != 1 {
			t.Errorf("notification durable mail = %d, err=%v", len(box.Mails), err)
		}
		mailboxSuccessResponse(w, true)
	})
	jobID := strings.Repeat("a", 32)
	fixture = newExternalMailNotificationChain(t, client, state, jobID)

	status, first := fixture.runTask(t, jobID)
	if status != http.StatusOK {
		t.Fatalf("first run = %d %+v", status, first)
	}
	data := externalMailNotificationData(t, first)
	if data["sent"] != float64(1) || data["failed"] != float64(0) || runtimeCalls.Load() != 1 || fixture.gatewayCalls.Load() != 1 {
		t.Fatalf("delivery=%+v runtime_calls=%d gateway_calls=%d", data, runtimeCalls.Load(), fixture.gatewayCalls.Load())
	}
	if !state.wasCommitted() || !fixture.harness.hasEvent("gm-mailbox-notification") {
		t.Fatalf("commit/notification evidence missing: events=%v", fixture.harness.eventKinds())
	}

	status, replay := fixture.runTask(t, jobID)
	if status != http.StatusConflict || runtimeCalls.Load() != 1 {
		t.Fatalf("replay = %d runtime_calls=%d body=%+v", status, runtimeCalls.Load(), replay)
	}
	if failure, _ := replay["error"].(map[string]any); failure["code"] != "revision_conflict" || fixture.gatewayCalls.Load() != 2 {
		t.Fatalf("replay error/gateway calls = %+v/%d", replay, fixture.gatewayCalls.Load())
	}
	box, _, err := fixture.harness.group.Mailbox.Load(context.Background(), "1")
	if err != nil || len(box.Mails) != 1 {
		t.Fatalf("replay duplicated mail: mails=%d err=%v", len(box.Mails), err)
	}
}

func TestExternalMailV1NotificationFailureKeepsTaskCommitted(t *testing.T) {
	state := &benefitRunSQLState{}
	reached := make(chan struct{}, 1)
	client := mailboxNotificationServer(t, func(request mailboxNotificationRequest, w http.ResponseWriter, _ *http.Request) {
		if request.Action != notifyMailboxAction || request.CharacterID != 1 {
			t.Errorf("notification request = %+v", request)
		}
		reached <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(adminapi.Response{Version: adminapi.Version, OK: false, Error: "fixture failure"})
	})
	jobID := strings.Repeat("b", 32)
	fixture := newExternalMailNotificationChain(t, client, state, jobID)

	status, run := fixture.runTask(t, jobID)
	select {
	case <-reached:
	default:
		t.Fatal("notification failure fixture was not reached")
	}
	if status != http.StatusOK {
		t.Fatalf("notification failure was misreported as task failure: %d %+v", status, run)
	}
	data := externalMailNotificationData(t, run)
	if data["sent"] != float64(1) || !state.wasCommitted() || fixture.gatewayCalls.Load() != 1 {
		t.Fatalf("committed task=%+v committed=%v gateway_calls=%d", data, state.wasCommitted(), fixture.gatewayCalls.Load())
	}
	box, _, err := fixture.harness.group.Mailbox.Load(context.Background(), "1")
	if err != nil || len(box.Mails) != 1 || !fixture.harness.hasEvent("gm-mailbox-notification-failed") {
		t.Fatalf("failure changed committed mail or diagnostic: mails=%d err=%v events=%v", len(box.Mails), err, fixture.harness.eventKinds())
	}
}
