package alert

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/relay-monitor/relay/internal/db"
	"github.com/relay-monitor/relay/internal/notify"
	"github.com/relay-monitor/relay/internal/state"
)

const defaultCooldown = 10 * time.Minute

// Dispatcher delivers alerts to all configured channels with cooldown logic.
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

// Dispatch sends the event to every configured alert channel (respecting cooldown).
func (d *Dispatcher) Dispatch(evt state.Event) {
	// Recovery alerts always send; only outgoing alerts are throttled.
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

	channels, err := d.db.ListAlertChannels()
	if err != nil {
		log.Printf("alert: list channels: %v", err)
		return
	}

	payload := notify.Payload{
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
		payload.IncidentID = evt.Incident.ID
		payload.IncidentTitle = evt.Incident.Title
	}

	for _, ch := range channels {
		var cfg map[string]any
		if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil {
			log.Printf("alert: parse channel config %s: %v", ch.ID, err)
			continue
		}
		if err := send(ch.Type, cfg, payload); err != nil {
			log.Printf("alert: send via %s (%s): %v", ch.Type, ch.Name, err)
		}
	}
}

func send(chanType string, cfg map[string]any, p notify.Payload) error {
	switch chanType {
	case "webhook":
		return notify.SendWebhook(cfg, p)
	case "slack":
		return notify.SendSlack(cfg, p)
	case "email":
		return notify.SendSMTP(cfg, p)
	default:
		log.Printf("alert: unknown channel type %q, skipping", chanType)
		return nil
	}
}
