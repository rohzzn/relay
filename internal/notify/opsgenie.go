package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SendOpsGenie sends an alert via the OpsGenie Alerts API.
// cfg keys: api_key (required), region ("eu" for EU endpoint, default US)
func SendOpsGenie(cfg map[string]any, p Payload) error {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return fmt.Errorf("opsgenie: api_key not configured")
	}

	baseURL := "https://api.opsgenie.com/v2/alerts"
	if region, _ := cfg["region"].(string); region == "eu" {
		baseURL = "https://api.eu.opsgenie.com/v2/alerts"
	}

	if p.EventType == "up" {
		// Close the alert by alias.
		return opsgenieClose(apiKey, baseURL, "relay-"+p.MonitorName)
	}

	priority := "P2"
	if p.EventType == "degraded" {
		priority = "P3"
	}

	payload := map[string]any{
		"message":  p.Subject(),
		"alias":    "relay-" + p.MonitorName,
		"priority": priority,
		"source":   "Relay Monitor",
		"entity":   p.MonitorName,
		"tags":     []string{"relay", p.MonitorType},
		"details": map[string]string{
			"monitor":    p.MonitorName,
			"target":     p.Target,
			"type":       p.MonitorType,
			"detail":     p.Detail,
			"latency_ms": fmt.Sprintf("%d", p.LatencyMs),
			"time":       p.Time.Format(time.RFC3339),
		},
		"description": fmt.Sprintf("%s\nTarget: %s\nDetail: %s", p.Subject(), p.Target, p.Detail),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+apiKey)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("opsgenie POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("opsgenie returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func opsgenieClose(apiKey, baseURL, alias string) error {
	body, _ := json.Marshal(map[string]string{"source": "Relay Monitor"})
	url := fmt.Sprintf("%s/%s/close?identifierType=alias", baseURL, alias)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+apiKey)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("opsgenie close: %w", err)
	}
	defer resp.Body.Close()
	return nil
}
