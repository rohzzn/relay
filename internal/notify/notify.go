package notify

import "time"

// Payload is the normalized event data sent to all notification channels.
type Payload struct {
	EventType     string    // "down", "up", "degraded"
	MonitorName   string
	MonitorType   string
	Target        string
	Status        string
	Detail        string
	LatencyMs     int64
	Time          time.Time
	IncidentID    string
	IncidentTitle string
}

func (p Payload) Emoji() string {
	switch p.EventType {
	case "up":
		return "✅"
	case "down":
		return "🔴"
	case "degraded":
		return "🟡"
	default:
		return "⚪"
	}
}

func (p Payload) Subject() string {
	switch p.EventType {
	case "up":
		return "[Resolved] " + p.MonitorName + " is back up"
	case "down":
		return "[Down] " + p.MonitorName + " is unreachable"
	case "degraded":
		return "[Degraded] " + p.MonitorName + " is responding slowly"
	default:
		return "[Alert] " + p.MonitorName
	}
}
