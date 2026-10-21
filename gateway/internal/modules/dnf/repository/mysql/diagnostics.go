package mysql

import (
	"context"
	"database/sql"
	"dnf90.local/gateway/internal/diagnostics"
)

func diagnosticQuery(ctx context.Context, db SQLDB, phase, query string, args ...any) (SQLRows, error) {
	if !diagnostics.Enabled(ctx) {
		return db.QueryContext(ctx, query, args...)
	}
	switch d := db.(type) {
	case SQLDBAdapter:
		return diagnostics.Query(ctx, d.DB, phase, query, args...)
	case sqlTransactionDB:
		return diagnostics.Query(ctx, d.tx, phase, query, args...)
	default:
		return db.QueryContext(ctx, query, args...)
	}
}
func diagnosticRow(ctx context.Context, db SQLDB, phase, query string, args ...any) SQLRow {
	if !diagnostics.Enabled(ctx) {
		return db.QueryRowContext(ctx, query, args...)
	}
	switch d := db.(type) {
	case SQLDBAdapter:
		return diagnostics.QueryRow(ctx, d.DB, phase, query, args...)
	case sqlTransactionDB:
		return diagnostics.QueryRow(ctx, d.tx, phase, query, args...)
	default:
		return db.QueryRowContext(ctx, query, args...)
	}
}
func diagnosticBegin(ctx context.Context, db SQLDB, b sqlTxBeginner) (*sql.Tx, func(), error) {
	if a, ok := db.(SQLDBAdapter); ok {
		return diagnostics.Begin(ctx, a.DB)
	}
	return diagnostics.Begin(ctx, b)
}
