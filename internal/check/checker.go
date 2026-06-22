package check

import (
	"context"
	"time"
)

// Status constants for check results.
const (
	StatusUp       = "up"
	StatusDown     = "down"
	StatusDegraded = "degraded"
)

// Result holds the outcome of a single probe.
type Result struct {
	Status    string
	LatencyMs int64
	Detail    string
	CheckedAt int64
}

// Checker performs a single probe against a target.
type Checker interface {
	Check(ctx context.Context, target string, cfg map[string]any) Result
}

func resultDown(detail string) Result {
	return Result{Status: StatusDown, Detail: detail, CheckedAt: time.Now().Unix()}
}

func resultUp(latency time.Duration, detail string) Result {
	return Result{
		Status:    StatusUp,
		LatencyMs: latency.Milliseconds(),
		Detail:    detail,
		CheckedAt: time.Now().Unix(),
	}
}

func resultDegraded(latency time.Duration, detail string) Result {
	return Result{
		Status:    StatusDegraded,
		LatencyMs: latency.Milliseconds(),
		Detail:    detail,
		CheckedAt: time.Now().Unix(),
	}
}
