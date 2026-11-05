package mysql

import (
	"context"
	"errors"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

// A 1213 response means MySQL rolled back the entire transaction. Never replay
// a timeout, a lost connection, or an ambiguous commit response here.
func retryInventoryDeadlock(ctx context.Context, run func() error) error {
	for attempt := 0; ; attempt++ {
		err := run()
		var failure *driver.MySQLError
		if attempt == 2 || !errors.As(err, &failure) || failure.Number != 1213 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
