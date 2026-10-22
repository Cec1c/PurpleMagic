package gmtool

import (
	"context"
	"errors"
	"net/http"
	"time"

	"dnf90.local/gateway/internal/diagnostics"
	"dnf90.local/gateway/internal/servicehttp"
)

const BenefitReconciliationInterval = 3 * time.Hour

// One bounded, durable checkpoint replaces endless full-account scans. Rules
// enabled by an administrator request an immediate cycle; ordinary new accounts
// and characters are covered by the recurring sweep, including old accounts.
type benefitReconciliation struct {
	Status         string              `json:"status"`
	CycleID        string              `json:"cycleId"`
	Position       benefitScanPosition `json:"position"`
	Requested      uint64              `json:"requested"`
	RunningRequest uint64              `json:"runningRequest"`
	StartedAt      time.Time           `json:"startedAt"`
	UpdatedAt      time.Time           `json:"updatedAt"`
	CompletedAt    time.Time           `json:"completedAt"`
	NextAt         time.Time           `json:"nextAt"`
	Pages          int                 `json:"pages"`
	Checked        int                 `json:"checked"`
	Delivered      int                 `json:"delivered"`
	Received       int                 `json:"received"`
	Failed         int                 `json:"failed"`
	Blocked        []benefitScanBlock  `json:"blocked,omitempty"`
	Error          string              `json:"error,omitempty"`
}

func (q *ExternalQueue) ConfigureBenefitReconciliation(enabled bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.scanEnabled, q.scanInterval = enabled, BenefitReconciliationInterval
}

func (q *ExternalQueue) RequestBenefitReconciliation() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	old := q.state.Reconciliation
	r := benefitReconciliation{Status: "waiting"}
	if old != nil {
		r = *old
	}
	r.Requested++
	if r.Status != "active" {
		r.NextAt = time.Time{}
	}
	q.state.Reconciliation = &r
	if err := q.save(); err != nil {
		q.state.Reconciliation = old
		return err
	}
	return nil
}

func (q *ExternalQueue) HandleBenefitReconciliation(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/benefits/reconciliation" {
		return false
	}
	if r.Method != http.MethodGet {
		v1Fail(w, r, 405, "method_not_allowed", "请求方法无效", false)
		return true
	}
	q.mu.Lock()
	state := benefitReconciliation{Status: "waiting"}
	if q.state.Reconciliation != nil {
		state = *q.state.Reconciliation
	}
	enabled, interval := q.scanEnabled, q.scanInterval
	q.mu.Unlock()
	// The opaque resume position and scheduling counters stay on the service.
	v1Reply(w, r, 200, v1Envelope{Data: map[string]any{
		"enabled": enabled, "intervalSeconds": int(interval.Seconds()), "status": state.Status,
		"startedAt": v1Time(state.StartedAt), "updatedAt": v1Time(state.UpdatedAt), "completedAt": v1Time(state.CompletedAt), "nextAt": v1Time(state.NextAt),
		"pages": state.Pages, "checked": state.Checked, "delivered": state.Delivered, "received": state.Received, "failed": state.Failed, "blocked": state.Blocked, "error": state.Error,
	}})
	return true
}

// step persists the checkpoint before and after each request. If a response is
// lost, replay uses permanent receipts, so neither money nor mail is duplicated.
func (q *ExternalQueue) stepBenefitReconciliation(ctx context.Context, now time.Time, log func(string, ...any)) (bool, error) {
	q.scanMu.Lock()
	defer q.scanMu.Unlock()
	q.mu.Lock()
	if !q.scanEnabled {
		q.mu.Unlock()
		return false, nil
	}
	old := q.state.Reconciliation
	r := benefitReconciliation{Status: "waiting"}
	if old != nil {
		r = *old
	}
	if r.Status != "active" {
		if now.Before(r.NextAt) {
			q.mu.Unlock()
			return false, nil
		}
		r = benefitReconciliation{Status: "active", CycleID: diagnostics.ID(), Requested: r.Requested, RunningRequest: r.Requested, StartedAt: now, UpdatedAt: now, CompletedAt: r.CompletedAt}
		snapshot := r
		q.state.Reconciliation = &snapshot
		if err := q.save(); err != nil {
			q.state.Reconciliation = old
			q.mu.Unlock()
			return false, err
		}
	}
	interval := q.scanInterval
	q.mu.Unlock()
	run := diagnostics.New(ctx)
	diagnostics.Field(run, "cycle_id", r.CycleID)
	diagnostics.Field(run, "account_cursor", r.Position.Account)
	var out benefitScanResult
	err := q.peer.JSON(run, "/api/internal/gateway/v1/scan-v2", r.Position, &out)
	if err == nil && (out.Position.Account < 0 || len(out.Position.Character) > 128 || len(out.Position.Campaign) > 32 ||
		(out.Position.Character == "") != (out.Position.Campaign == "") || out.Checked < 0 || out.Delivered < 0 || out.Received < 0 || out.Failed < 0 || len(out.Blocked) > 1000 || (out.Done && out.Position != (benefitScanPosition{}))) {
		err = &servicehttp.PeerError{Kind: "invalid_benefit_scan"}
	}
	r.UpdatedAt = now
	if err != nil {
		r.Error = "连接或读取失败，后台将自动继续"
	} else {
		r.Error = ""
		r.Position = out.Position
		r.Pages++
		r.Checked += out.Checked
		r.Delivered += out.Delivered
		r.Received += out.Received
		r.Failed += out.Failed
		// Each page reports the current rule blockers, allowing a resolved rule to disappear.
		r.Blocked = out.Blocked
		if out.Retry {
			r.Error = "部分对象未发放，下轮校验自动重试"
		}
		if out.Done {
			r.Status = "waiting"
			r.CompletedAt = now
			r.NextAt = now.Add(interval)
			if r.Failed > 0 {
				r.Error = "部分对象未发放，下轮校验自动重试"
			}
		}
	}
	q.mu.Lock()
	old = q.state.Reconciliation
	if old != nil {
		r.Requested = old.Requested
	}
	if r.Status != "active" && r.Requested != r.RunningRequest {
		r.NextAt = now
	}
	q.state.Reconciliation = &r
	saveErr := q.save()
	if saveErr != nil {
		q.state.Reconciliation = old
	}
	q.mu.Unlock()
	if saveErr != nil {
		return false, saveErr
	}
	if err != nil {
		diagnostics.Log(log, run, "benefit-reconciliation-retry", err)
	} else if out.Done {
		diagnostics.Log(log, run, "benefit-reconciliation-complete", nil, "checked", r.Checked, "delivered", r.Delivered, "received", r.Received, "failed", r.Failed, "blocked_rules", len(r.Blocked))
	}
	return err == nil && !out.Done && !out.Retry, err
}

func (q *ExternalQueue) RunBenefitReconciliation(ctx context.Context, pageInterval time.Duration, log func(string, ...any)) error {
	if pageInterval < 5*time.Second {
		return errors.New("scan interval must be at least five seconds")
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		for i := 0; i < 10; i++ {
			more, err := q.stepBenefitReconciliation(ctx, time.Now().UTC(), log)
			if err != nil && ctx.Err() == nil {
				diagnostics.Log(log, ctx, "benefit-reconciliation-step-failed", err)
			}
			if !more || ctx.Err() != nil {
				break
			}
		}
		timer.Reset(pageInterval)
	}
}
