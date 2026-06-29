package db

import "database/sql"

// Monitor represents a configured uptime check.
type Monitor struct {
	ID           string
	Name         string
	Type         string // http, tcp, dns, tls, heartbeat
	Target       string // URL, host:port, domain
	IntervalS    int
	Regions      string // JSON array of probe IDs, default "local"
	Config       string // JSON: type-specific options
	Status       string // pending, up, down, degraded
	GroupName    string // optional display group for dashboard / status page
	Paused       bool
	CreatedAt    int64
}

// Check is a single probe result for a monitor.
type Check struct {
	ID        int64
	MonitorID string
	Region    string
	Status    string // up, down, degraded
	LatencyMs sql.NullInt64
	Detail    sql.NullString
	CheckedAt int64
}

// Incident represents a downtime event (auto-opened or manual).
type Incident struct {
	ID         string
	MonitorID  sql.NullString
	Title      string
	Body       sql.NullString
	Status     string // investigating, identified, monitoring, resolved
	StartedAt  int64
	ResolvedAt sql.NullInt64
}

// Subscriber is someone who has opted in to status-page email notifications.
type Subscriber struct {
	ID        string
	Email     string
	Token     string
	Monitors  sql.NullString // JSON array; NULL = all monitors
	Confirmed int
	CreatedAt int64
}

// AlertChannel is a delivery target for operational alerts.
type AlertChannel struct {
	ID     string
	Name   string
	Type   string // slack, email, webhook, pagerduty, opsgenie
	Config string // JSON
}

// DayUptime holds an aggregate uptime percentage for a calendar day.
type DayUptime struct {
	Date      string
	Status    string
	UptimePct float64
}

// User is a team member who can log in to the admin dashboard.
type User struct {
	ID         string
	Username   string
	Email      string
	HashedPass string
	Role       string // admin, editor, viewer
	CreatedAt  int64
}

// APIKey is a bearer token for the REST API.
type APIKey struct {
	ID         string
	Name       string
	KeyHash    string
	Role       string // admin, editor, viewer
	CreatedAt  int64
	LastUsedAt sql.NullInt64
}

// MaintenanceWindow suppresses alerts for a monitor (or all monitors) during a time range.
type MaintenanceWindow struct {
	ID        string
	Label     string
	MonitorID sql.NullString // NULL = all monitors
	StartsAt  int64
	EndsAt    int64
	CreatedAt int64
}

// AuditEntry records an admin action.
type AuditEntry struct {
	ID         int64
	Actor      string
	Action     string
	EntityType string
	EntityID   string
	Detail     string
	CreatedAt  int64
}

// LatencyPoint is a time-bucketed latency data point for charting.
type LatencyPoint struct {
	Bucket    int64
	AvgMs     float64
	P95Ms     float64
	CheckCount int
}
