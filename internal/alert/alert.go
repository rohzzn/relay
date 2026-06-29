package alert

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/rohzzn/relay/internal/db"
	"github.com/rohzzn/relay/internal/notify"
	"github.com/rohzzn/relay/internal/state"
)

const defaultCooldown = 10 * time.Minute

// Dispatcher delivers alerts to configured channels with cooldown and per-monitor routing.
type Dispatcher struct {
	db        *db.DB
	cooldowns map[string]time.Time
	mu        sync.Mutex
}

func New(database *db.DB) *Dispatcher {
	return &Dispatcher{
		db:        database,
		cooldowns: make(map[string]time.Time),
	}
}

// Dispatch sends the event to the channels routed for this monitor (respecting cooldown).
func (d *Dispatcher) Dispatch(evt state.Event) {
	// Recovery alerts always send; outgoing alerts respect cooldown.
	if evt.Type != state.EventUp {
		d.mu.Lock()
		last := d.cooldowns[evt.Monitor.ID]
		if time.Since(last) < defaultCooldown {
			d.mu.Unlock()
			return
		}
		d.cooldowns[evt.Monitor.ID] = time.Now()
		d.mu.Unlock()
	}

	// Per-monitor routing: use assigned channels or fall back to all.
	channels, err := d.db.GetChannelsForMonitor(evt.Monitor.ID)
	if err != nil {
		log.Printf("alert: get channels for %s: %v", evt.Monitor.ID, err)
		return
	}

	payload := buildPayload(evt)
	for _, ch := range channels {
		if err := sendToChannel(ch, payload); err != nil {
			log.Printf("alert: send via %s (%s): %v", ch.Type, ch.Name, err)
		}
	}
}

// TestChannel sends a test payload to a single channel and returns any error.
func (d *Dispatcher) TestChannel(channelID string) error {
	ch, err := d.db.GetAlertChannel(channelID)
	if err != nil || ch == nil {
		return err
	}
	p := notify.Payload{
		EventType:   "down",
		MonitorName: "Test Monitor",
		MonitorType: "http",
		Target:      "https://example.com",
		Status:      "down",
		Detail:      "This is a test alert from Relay.",
		LatencyMs:   0,
		Time:        time.Now(),
	}
	return sendToChannel(ch, p)
}

func buildPayload(evt state.Event) notify.Payload {
	p := notify.Payload{
		EventType:   string(evt.Type),
		MonitorName: evt.Monitor.Name,
		MonitorType: evt.Monitor.Type,
		Target:      evt.Monitor.Target,
		Status:      evt.Result.Status,
		Detail:      evt.Result.Detail,
		LatencyMs:   evt.Result.LatencyMs,
		Time:        time.Unix(evt.Result.CheckedAt, 0),
	}
	if evt.Incident != nil {
		p.IncidentID = evt.Incident.ID
		p.IncidentTitle = evt.Incident.Title
	}
	return p
}

func sendToChannel(ch *db.AlertChannel, p notify.Payload) error {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil {
		return err
	}
	switch ch.Type {
	case "webhook":
		return notify.SendWebhook(cfg, p)
	case "slack":
		return notify.SendSlack(cfg, p)
	case "email":
		return notify.SendSMTP(cfg, p)
	case "pagerduty":
		return notify.SendPagerDuty(cfg, p)
	case "opsgenie":
		return notify.SendOpsGenie(cfg, p)
	default:
		log.Printf("alert: unknown channel type %q, skipping", ch.Type)
		return nil
	}
}
