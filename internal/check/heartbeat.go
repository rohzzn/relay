package check

import (
	"context"
	"fmt"
	"time"
)

// Heartbeat checks whether a heartbeat ping was received within the monitor's interval.
// The DB is consulted via a callback to avoid an import cycle.
type Heartbeat struct {
	GetLastPing func(monitorID string) int64
}

func (h *Heartbeat) Check(ctx context.Context, target string, cfg map[string]any) Result {
	// target is the monitor ID
	graceMultiplier := intCfg(cfg, "grace_multiplier", 2)
	intervalS := intCfg(cfg, "interval_s", 60)

	lastPing := h.GetLastPing(target)
	if lastPing == 0 {
		return Result{
			Status:    StatusPending,
			Detail:    "no heartbeat received yet",
			CheckedAt: time.Now().Unix(),
		}
	}

	deadline := lastPing + int64(intervalS*graceMultiplier)
	if time.Now().Unix() > deadline {
		missedFor := time.Since(time.Unix(lastPing, 0)).Round(time.Second)
		return resultDown(fmt.Sprintf("no heartbeat for %s", missedFor))
	}

	age := time.Since(time.Unix(lastPing, 0)).Round(time.Second)
	return Result{
		Status:    StatusUp,
		LatencyMs: 0,
		Detail:    fmt.Sprintf("last ping %s ago", age),
		CheckedAt: time.Now().Unix(),
	}
}

const StatusPending = "pending"
