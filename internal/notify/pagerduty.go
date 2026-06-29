package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SendPagerDuty sends an alert via the PagerDuty Events API v2.
// cfg keys: integration_key (required), severity (optional, default "error")
func SendPagerDuty(cfg map[string]any, p Payload) error {
	key, _ := cfg["integration_key"].(string)
	if key == "" {
		return fmt.Errorf("pagerduty: integration_key not configured")
	}

	severity := "error"
	if p.EventType == "degraded" {
		severity = "warning"
	}
	if p.EventType == "up" {
		severity = "info"
	}
	if s, ok := cfg["severity"].(string); ok && s != "" {
		severity = s
	}

	action := "trigger"
	if p.EventType == "up" {
		action = "resolve"
	}

	payload := map[string]any{
		"routing_key":  key,
		"event_action": action,
		"dedup_key":    "relay-" + p.MonitorName,
		"payload": map[string]any{
			"summary":   p.Subject(),
			"source":    p.Target,
			"severity":  severity,
			"timestamp": p.Time.Format(time.RFC3339),
			"custom_details": map[string]any{
				"monitor":    p.MonitorName,
				"type":       p.MonitorType,
				"target":     p.Target,
				"detail":     p.Detail,
				"latency_ms": p.LatencyMs,
			},
		},
		"links": []map[string]string{
			{"href": p.Target, "text": "Monitor Target"},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Post(
		"https://events.pagerduty.com/v2/enqueue",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("pagerduty POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pagerduty returned HTTP %d", resp.StatusCode)
	}
	return nil
}
