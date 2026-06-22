package check

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTP checks an HTTP/HTTPS endpoint.
type HTTP struct{}

func (h *HTTP) Check(ctx context.Context, target string, cfg map[string]any) Result {
	method := stringCfg(cfg, "method", "GET")
	timeout := durationCfg(cfg, "timeout_s", 30)
	followRedirects := boolCfg(cfg, "follow_redirects", true)
	expectedStatus := intCfg(cfg, "expected_status", 0)
	keyword := stringCfg(cfg, "keyword", "")
	maxLatencyMs := intCfg(cfg, "max_latency_ms", 0)

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
		},
	}
	if !followRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return resultDown(fmt.Sprintf("invalid request: %v", err))
	}
	req.Header.Set("User-Agent", "Relay-Monitor/1.0")

	if headers, ok := cfg["headers"].(map[string]any); ok {
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		return resultDown(err.Error())
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB max

	detail := fmt.Sprintf("HTTP %d", resp.StatusCode)

	// Keyword check.
	if keyword != "" && !strings.Contains(string(body), keyword) {
		return resultDegraded(latency, fmt.Sprintf("keyword %q not found in response", keyword))
	}

	// Status code check.
	if expectedStatus > 0 && resp.StatusCode != expectedStatus {
		return resultDown(fmt.Sprintf("expected HTTP %d, got %d", expectedStatus, resp.StatusCode))
	}
	if expectedStatus == 0 && resp.StatusCode >= 400 {
		return resultDown(detail)
	}

	// Latency threshold check.
	if maxLatencyMs > 0 && latency.Milliseconds() > int64(maxLatencyMs) {
		return resultDegraded(latency, fmt.Sprintf("response time %dms exceeds threshold %dms", latency.Milliseconds(), maxLatencyMs))
	}

	return resultUp(latency, detail)
}

func stringCfg(cfg map[string]any, key, def string) string {
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func intCfg(cfg map[string]any, key string, def int) int {
	if v, ok := cfg[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		}
	}
	return def
}

func boolCfg(cfg map[string]any, key string, def bool) bool {
	if v, ok := cfg[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func durationCfg(cfg map[string]any, key string, defSeconds int) time.Duration {
	s := intCfg(cfg, key, defSeconds)
	return time.Duration(s) * time.Second
}
