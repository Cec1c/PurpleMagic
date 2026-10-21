package diagnostics

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"

	mysql "github.com/go-sql-driver/mysql"
)

func sha256Sum(value string) [32]byte { return sha256.Sum256([]byte(value)) }

type errorMetadata interface{ DiagnosticFields() []any }

// Preserve the error chain for classification, never print arbitrary Error()
// strings: drivers can embed SQL values and HTTP errors can embed full URLs.
func ErrorFields(err error) []any {
	if err == nil {
		return []any{"error_kind", "none"}
	}
	kind := "internal"
	var n net.Error
	var db *mysql.MySQLError
	var op *net.OpError
	var errno syscall.Errno
	var certificate *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.As(err, &db):
		kind = "database"
	case errors.As(err, &certificate):
		kind = "tls_certificate"
	case errors.As(err, &n):
		if n.Timeout() {
			kind = "timeout"
		} else {
			kind = "network"
		}
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		kind = "response_eof"
	}
	out := []any{"error_kind", kind, "cause_type", fmt.Sprintf("%T", err), "canceled", errors.Is(err, context.Canceled), "timed_out", kind == "timeout"}
	chain := []string{}
	for cause := err; cause != nil && len(chain) < 8; cause = errors.Unwrap(cause) {
		chain = append(chain, fmt.Sprintf("%T", cause))
	}
	out = append(out, "cause_types", chain)
	if db != nil {
		out = append(out, "mysql_number", db.Number, "mysql_state", string(db.SQLState[:]))
	}
	if errors.As(err, &op) {
		out = append(out, "network_operation", op.Op)
	}
	if errors.As(err, &errno) {
		out = append(out, "errno", int(errno))
	}
	var meta errorMetadata
	if errors.As(err, &meta) {
		out = append(out, meta.DiagnosticFields()...)
	}
	return out
}
