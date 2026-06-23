package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SendSlack posts a formatted message to a Slack incoming webhook URL.
func SendSlack(cfg map[string]any, p Payload) error {
	url, _ := cfg["url"].(string)
	if url == "" {
		return fmt.Errorf("slack: webhook url not configured")
	}

	color := "#e53e3e"
	switch p.EventType {
	case "up":
		color = "#38a169"
	case "degraded":
		color = "#d69e2e"
	}

	msg := map[string]any{
		"attachments": []map[string]any{
			{
				"color":      color,
				"title":      p.Emoji() + " " + p.Subject(),
				"text":       p.Detail,
				"footer":     "Relay Monitor",
				"footer_icon": "https://raw.githubusercontent.com/rohzzn/relay/main/web/static/favicon.svg",
				"ts":         p.Time.Unix(),
				"fields": []map[string]any{
					{"title": "Monitor", "value": p.MonitorName, "short": true},
					{"title": "Target", "value": p.Target, "short": true},
					{"title": "Status", "value": p.Status, "short": true},
					{"title": "Latency", "value": fmt.Sprintf("%dms", p.LatencyMs), "short": true},
				},
			},
		},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack returned HTTP %d", resp.StatusCode)
	}
	return nil
}
