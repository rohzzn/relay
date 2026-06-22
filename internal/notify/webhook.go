package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SendWebhook POSTs a JSON payload to the configured URL.
func SendWebhook(cfg map[string]any, p Payload) error {
	url, _ := cfg["url"].(string)
	if url == "" {
		return fmt.Errorf("webhook: url not configured")
	}

	body, err := json.Marshal(map[string]any{
		"event":      p.EventType,
		"monitor":    p.MonitorName,
		"type":       p.MonitorType,
		"target":     p.Target,
		"status":     p.Status,
		"detail":     p.Detail,
		"latency_ms": p.LatencyMs,
		"time":       p.Time.Format(time.RFC3339),
		"incident_id": p.IncidentID,
	})
	if err != nil {
		return err
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
