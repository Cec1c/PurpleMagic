package gmtool

import (
	"context"
	"dnf90.local/gateway/internal/diagnostics"
)

// Carries only bounded diagnostic metadata; it does not alter queue state,
// idempotency keys, reward identity or the retry decision.
type jobError struct {
	err    error
	fields []any
}

func (e *jobError) Error() string           { return "background GM job failed" }
func (e *jobError) Unwrap() error           { return e.err }
func (e *jobError) DiagnosticFields() []any { return e.fields }
func backgroundJob(ctx context.Context, kind, id string) context.Context {
	ctx = diagnostics.New(ctx)
	diagnostics.Field(ctx, "job_kind", kind)
	diagnostics.Field(ctx, "job_id", id)
	return ctx
}
func wrapJob(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	diagnostics.Fail(ctx, err)
	return &jobError{err: err, fields: diagnostics.Fields(ctx)}
}
