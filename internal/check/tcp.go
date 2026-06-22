package check

import (
	"context"
	"fmt"
	"net"
	"time"
)

// TCP checks raw TCP connectivity to a host:port.
type TCP struct{}

func (t *TCP) Check(ctx context.Context, target string, cfg map[string]any) Result {
	timeout := durationCfg(cfg, "timeout_s", 10)

	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", target)
	latency := time.Since(start)

	if err != nil {
		return resultDown(fmt.Sprintf("TCP dial failed: %v", err))
	}
	conn.Close()
	return resultUp(latency, fmt.Sprintf("TCP connected in %dms", latency.Milliseconds()))
}
