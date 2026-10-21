package diagnostics

import (
	"context"
	"database/sql"
	"sync"
)

type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type Beginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}
type Row struct {
	ctx          context.Context
	db           Queryer
	phase, query string
	args         []any
}

func QueryRow(ctx context.Context, db Queryer, phase, query string, args ...any) *Row {
	return &Row{ctx, db, phase, query, args}
}
func (r *Row) Scan(dest ...any) error {
	rows, e := Query(r.ctx, r.db, r.phase, r.query, r.args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	if !rows.Next() {
		if e = rows.Err(); e != nil {
			return e
		}
		return sql.ErrNoRows
	}
	if e = rows.Scan(dest...); e != nil {
		return e
	}
	return rows.Close()
}

type Rows struct {
	*sql.Rows
	finish  func(error)
	release func()
	scanErr error
}

func (r *Rows) Scan(dest ...any) error {
	e := r.Rows.Scan(dest...)
	if e != nil {
		r.scanErr = e
	}
	return e
}

func (r *Rows) Close() error {
	e := r.Rows.Close()
	err := r.Rows.Err()
	if err == nil {
		err = e
	}
	if err == nil {
		err = r.scanErr
	}
	r.finish(err)
	r.release()
	return e
}
func (r *Rows) Next() bool {
	ok := r.Rows.Next()
	if !ok {
		r.Close()
	}
	return ok
}

// Connection acquisition includes pool wait, dialing and connection validation.
// Query/row time includes execution, server lock waits, transfer and scanning.
func Query(ctx context.Context, db Queryer, phase, query string, args ...any) (*Rows, error) {
	release := func() {}
	if pool, ok := db.(*sql.DB); ok && Enabled(ctx) {
		done := Start(ctx, phase+".connection")
		conn, e := pool.Conn(ctx)
		done(e)
		if e != nil {
			return nil, e
		}
		db = conn
		var once sync.Once
		release = func() {
			once.Do(func() { conn.Close() })
		}
	}
	done := Start(ctx, phase+".query_rows")
	rows, e := db.QueryContext(ctx, query, args...)
	if e != nil {
		done(e)
		release()
		return nil, e
	}
	return &Rows{Rows: rows, finish: done, release: release}, nil
}
func Begin(ctx context.Context, db Beginner) (*sql.Tx, func(), error) {
	release := func() {}
	phase := "delivery.begin_including_connection"
	if pool, ok := db.(*sql.DB); ok && Enabled(ctx) {
		done := Start(ctx, "delivery.connection")
		conn, e := pool.Conn(ctx)
		done(e)
		if e != nil {
			return nil, release, e
		}
		db = conn
		release = func() { conn.Close() }
		phase = "delivery.begin"
	}
	done := Start(ctx, phase)
	tx, e := db.BeginTx(ctx, nil)
	done(e)
	if e != nil {
		release()
		return nil, func() {}, e
	}
	return tx, release, nil
}
