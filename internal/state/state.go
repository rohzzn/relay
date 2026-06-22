package state

import (
	"fmt"
	"sync"
	"time"

	"github.com/relay-monitor/relay/internal/check"
	"github.com/relay-monitor/relay/internal/db"
)

// EventType classifies a state transition.
type EventType string

const (
	EventDown     EventType = "down"
	EventUp       EventType = "up"
	EventDegraded EventType = "degraded"
)

// Event is emitted when a monitor's status changes.
type Event struct {
	Type      EventType
	Monitor   *db.Monitor
	Result    check.Result
	Incident  *db.Incident // non-nil when an incident was auto-opened or closed
}

// Manager tracks per-monitor state and produces events on transitions.
type Manager struct {
	db      *db.DB
	states  map[string]string // monitorID → last known status
	mu      sync.Mutex
	onEvent func(Event)
}

func New(database *db.DB, onEvent func(Event)) *Manager {
	return &Manager{
		db:      database,
		states:  make(map[string]string),
		onEvent: onEvent,
	}
}

// Record processes a check result and emits an event if the status changed.
func (m *Manager) Record(monitor *db.Monitor, result check.Result) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prev, known := m.states[monitor.ID]
	m.states[monitor.ID] = result.Status

	if known && prev == result.Status {
		return // no change
	}

	// Update the DB status.
	m.db.UpdateMonitorStatus(monitor.ID, result.Status)

	evt := Event{
		Monitor: monitor,
		Result:  result,
	}

	switch result.Status {
	case check.StatusDown:
		evt.Type = EventDown
		inc := m.openIncident(monitor, result)
		evt.Incident = inc

	case check.StatusDegraded:
		evt.Type = EventDegraded
		// Open an incident if there isn't one already.
		if existing, _ := m.db.GetActiveIncidentForMonitor(monitor.ID); existing == nil {
			inc := m.openIncident(monitor, result)
			evt.Incident = inc
		}

	case check.StatusUp:
		evt.Type = EventUp
		if prev == check.StatusDown || prev == check.StatusDegraded {
			inc := m.closeIncident(monitor)
			evt.Incident = inc
		}
	}

	if m.onEvent != nil {
		m.onEvent(evt)
	}
}

func (m *Manager) openIncident(monitor *db.Monitor, result check.Result) *db.Incident {
	inc := &db.Incident{
		Title:     fmt.Sprintf("%s is %s", monitor.Name, result.Status),
		Status:    "investigating",
		StartedAt: time.Now().Unix(),
	}
	inc.MonitorID.Valid = true
	inc.MonitorID.String = monitor.ID
	if result.Detail != "" {
		inc.Body.Valid = true
		inc.Body.String = result.Detail
	}
	if err := m.db.CreateIncident(inc); err != nil {
		return nil
	}
	return inc
}

func (m *Manager) closeIncident(monitor *db.Monitor) *db.Incident {
	inc, err := m.db.GetActiveIncidentForMonitor(monitor.ID)
	if err != nil || inc == nil {
		return nil
	}
	inc.Status = "resolved"
	inc.ResolvedAt.Valid = true
	inc.ResolvedAt.Int64 = time.Now().Unix()
	m.db.UpdateIncident(inc)
	return inc
}
