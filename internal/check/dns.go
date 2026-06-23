package check

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// DNS resolves a hostname and optionally validates the returned IP address.
type DNS struct{}

func (d *DNS) Check(ctx context.Context, target string, cfg map[string]any) Result {
	timeout := durationCfg(cfg, "timeout_s", 10)
	expectedIP := stringCfg(cfg, "expected_ip", "")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	addrs, err := (&net.Resolver{}).LookupHost(ctx, target)
	latency := time.Since(start)

	if err != nil {
		return resultDown(fmt.Sprintf("DNS lookup failed: %v", err))
	}
	if len(addrs) == 0 {
		return resultDown("DNS returned no addresses")
	}

	if expectedIP != "" {
		for _, addr := range addrs {
			if strings.EqualFold(addr, expectedIP) {
				return resultUp(latency, fmt.Sprintf("resolved → %s", addr))
			}
		}
		return resultDegraded(latency, fmt.Sprintf("expected %s not in response %v", expectedIP, addrs))
	}

	return resultUp(latency, fmt.Sprintf("resolved → %s", addrs[0]))
}
