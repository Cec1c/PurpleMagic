package mysql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	driver "github.com/go-sql-driver/mysql"
)

func TestInventoryDeadlockRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  error
		attempts int
	}{
		{"deadlock", fmt.Errorf("transaction: %w", &driver.MySQLError{Number: 1213}), 3},
		{"lock-timeout", &driver.MySQLError{Number: 1205}, 1},
		{"unknown-commit", io.ErrUnexpectedEOF, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			err := retryInventoryDeadlock(context.Background(), func() error { n++; return tc.failure })
			if n != tc.attempts || !errors.Is(err, tc.failure) {
				t.Fatal(n, err)
			}
		})
	}
	n := 0
	if err := retryInventoryDeadlock(context.Background(), func() error {
		n++
		if n == 1 {
			return &driver.MySQLError{Number: 1213}
		}
		return nil
	}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := retryInventoryDeadlock(ctx, func() error { return &driver.MySQLError{Number: 1213} }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
