package repository

import (
	"context"
	"time"
)

// DashboardCounts contains scalar aggregates; nil means unavailable, not zero.
type DashboardCounts struct {
	Characters, Clears, Fatigue, FatigueSamples, FastClears *int64
}
type DashboardReader interface {
	DashboardCounts(context.Context, time.Time, time.Time, int64) (DashboardCounts, error)
}
