package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite connection and exposes typed query methods.
type DB struct {
	db *sql.DB
}

// Open creates the data directory, opens the SQLite database with WAL mode,
// and runs the schema migration.
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	path := filepath.Join(dataDir, "relay.db")
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1&_cache_size=-8000"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite performs best with a single writer.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	d := &DB{db: sqlDB}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return d, nil
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) migrate() error {
	_, err := d.db.Exec(schema)
	return err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func now() int64 { return time.Now().Unix() }

func newID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// ── Monitors ─────────────────────────────────────────────────────────────────

func (d *DB) CreateMonitor(m *Monitor) error {
	m.ID = newID()
	m.CreatedAt = now()
	m.Status = "pending"
	_, err := d.db.Exec(
		`INSERT INTO monitors (id,name,type,target,interval_s,regions,config,status,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Name, m.Type, m.Target, m.IntervalS, m.Regions, m.Config, m.Status, m.CreatedAt,
	)
	return err
}

func (d *DB) GetMonitor(id string) (*Monitor, error) {
	m := &Monitor{}
	err := d.db.QueryRow(
		`SELECT id,name,type,target,interval_s,regions,config,status,created_at FROM monitors WHERE id=?`, id,
	).Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalS, &m.Regions, &m.Config, &m.Status, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

func (d *DB) ListMonitors() ([]*Monitor, error) {
	rows, err := d.db.Query(
		`SELECT id,name,type,target,interval_s,regions,config,status,created_at FROM monitors ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMonitors(rows)
}

func (d *DB) UpdateMonitor(m *Monitor) error {
	_, err := d.db.Exec(
		`UPDATE monitors SET name=?,type=?,target=?,interval_s=?,regions=?,config=? WHERE id=?`,
		m.Name, m.Type, m.Target, m.IntervalS, m.Regions, m.Config, m.ID,
	)
	return err
}

func (d *DB) UpdateMonitorStatus(id, status string) error {
	_, err := d.db.Exec(`UPDATE monitors SET status=? WHERE id=?`, status, id)
	return err
}

func (d *DB) DeleteMonitor(id string) error {
	_, err := d.db.Exec(`DELETE FROM monitors WHERE id=?`, id)
	return err
}

// ── Checks ───────────────────────────────────────────────────────────────────

func (d *DB) CreateCheck(c *Check) error {
	_, err := d.db.Exec(
		`INSERT INTO checks (monitor_id,region,status,latency_ms,detail,checked_at)
		 VALUES (?,?,?,?,?,?)`,
		c.MonitorID, c.Region, c.Status, c.LatencyMs, c.Detail, c.CheckedAt,
	)
	return err
}

func (d *DB) ListChecks(monitorID string, limit int) ([]*Check, error) {
	rows, err := d.db.Query(
		`SELECT id,monitor_id,region,status,latency_ms,detail,checked_at
		 FROM checks WHERE monitor_id=? ORDER BY checked_at DESC LIMIT ?`,
		monitorID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChecks(rows)
}

func (d *DB) GetLatestCheck(monitorID string) (*Check, error) {
	c := &Check{}
	err := d.db.QueryRow(
		`SELECT id,monitor_id,region,status,latency_ms,detail,checked_at
		 FROM checks WHERE monitor_id=? ORDER BY checked_at DESC LIMIT 1`,
		monitorID,
	).Scan(&c.ID, &c.MonitorID, &c.Region, &c.Status, &c.LatencyMs, &c.Detail, &c.CheckedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// GetDailyUptime returns per-day uptime percentages for the last N days.
func (d *DB) GetDailyUptime(monitorID string, days int) ([]DayUptime, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	rows, err := d.db.Query(`
		SELECT
			strftime('%Y-%m-%d', checked_at, 'unixepoch') AS day,
			COUNT(*) AS total,
			SUM(CASE WHEN status='up' THEN 1 ELSE 0 END) AS up_count
		FROM checks
		WHERE monitor_id=? AND checked_at >= ?
		GROUP BY day
		ORDER BY day ASC`,
		monitorID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]DayUptime)
	for rows.Next() {
		var du DayUptime
		var total, upCount int
		if err := rows.Scan(&du.Date, &total, &upCount); err != nil {
			return nil, err
		}
		if total > 0 {
			du.UptimePct = float64(upCount) / float64(total) * 100
		}
		du.Status = statusForPct(du.UptimePct)
		m[du.Date] = du
	}

	result := make([]DayUptime, days)
	for i := 0; i < days; i++ {
		day := time.Now().AddDate(0, 0, -(days-1-i)).Format("2006-01-02")
		if du, ok := m[day]; ok {
			result[i] = du
		} else {
			result[i] = DayUptime{Date: day, Status: "unknown", UptimePct: -1}
		}
	}
	return result, nil
}

func statusForPct(pct float64) string {
	switch {
	case pct >= 99:
		return "up"
	case pct >= 50:
		return "degraded"
	default:
		return "down"
	}
}

func (d *DB) GetUptimePct(monitorID string, days int) float64 {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	var total, upCount int
	d.db.QueryRow(
		`SELECT COUNT(*), SUM(CASE WHEN status='up' THEN 1 ELSE 0 END)
		 FROM checks WHERE monitor_id=? AND checked_at >= ?`,
		monitorID, cutoff,
	).Scan(&total, &upCount)
	if total == 0 {
		return -1
	}
	return float64(upCount) / float64(total) * 100
}

func (d *DB) GetAvgLatency(monitorID string, hours int) int64 {
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	var avg sql.NullFloat64
	d.db.QueryRow(
		`SELECT AVG(latency_ms) FROM checks
		 WHERE monitor_id=? AND checked_at >= ? AND latency_ms IS NOT NULL`,
		monitorID, cutoff,
	).Scan(&avg)
	if avg.Valid {
		return int64(avg.Float64)
	}
	return 0
}

func (d *DB) PruneChecks(retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()
	_, err := d.db.Exec(`DELETE FROM checks WHERE checked_at < ?`, cutoff)
	return err
}

// ── Incidents ────────────────────────────────────────────────────────────────

func (d *DB) CreateIncident(inc *Incident) error {
	inc.ID = newID()
	if inc.StartedAt == 0 {
		inc.StartedAt = now()
	}
	_, err := d.db.Exec(
		`INSERT INTO incidents (id,monitor_id,title,body,status,started_at,resolved_at)
		 VALUES (?,?,?,?,?,?,?)`,
		inc.ID, inc.MonitorID, inc.Title, inc.Body, inc.Status, inc.StartedAt, inc.ResolvedAt,
	)
	return err
}

func (d *DB) GetIncident(id string) (*Incident, error) {
	inc := &Incident{}
	err := d.db.QueryRow(
		`SELECT id,monitor_id,title,body,status,started_at,resolved_at FROM incidents WHERE id=?`, id,
	).Scan(&inc.ID, &inc.MonitorID, &inc.Title, &inc.Body, &inc.Status, &inc.StartedAt, &inc.ResolvedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return inc, err
}

func (d *DB) ListIncidents(activeOnly bool) ([]*Incident, error) {
	q := `SELECT id,monitor_id,title,body,status,started_at,resolved_at FROM incidents`
	if activeOnly {
		q += ` WHERE resolved_at IS NULL OR resolved_at=0`
	}
	q += ` ORDER BY started_at DESC`
	rows, err := d.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIncidents(rows)
}

func (d *DB) UpdateIncident(inc *Incident) error {
	_, err := d.db.Exec(
		`UPDATE incidents SET title=?,body=?,status=?,resolved_at=? WHERE id=?`,
		inc.Title, inc.Body, inc.Status, inc.ResolvedAt, inc.ID,
	)
	return err
}

func (d *DB) GetActiveIncidentForMonitor(monitorID string) (*Incident, error) {
	inc := &Incident{}
	err := d.db.QueryRow(
		`SELECT id,monitor_id,title,body,status,started_at,resolved_at
		 FROM incidents WHERE monitor_id=? AND (resolved_at IS NULL OR resolved_at=0)
		 ORDER BY started_at DESC LIMIT 1`,
		monitorID,
	).Scan(&inc.ID, &inc.MonitorID, &inc.Title, &inc.Body, &inc.Status, &inc.StartedAt, &inc.ResolvedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return inc, err
}

// ── Subscribers ──────────────────────────────────────────────────────────────

func (d *DB) CreateSubscriber(s *Subscriber) error {
	s.ID = newID()
	s.CreatedAt = now()
	_, err := d.db.Exec(
		`INSERT OR IGNORE INTO subscribers (id,email,token,monitors,confirmed,created_at)
		 VALUES (?,?,?,?,?,?)`,
		s.ID, s.Email, s.Token, s.Monitors, s.Confirmed, s.CreatedAt,
	)
	return err
}

func (d *DB) GetSubscriberByToken(token string) (*Subscriber, error) {
	s := &Subscriber{}
	err := d.db.QueryRow(
		`SELECT id,email,token,monitors,confirmed,created_at FROM subscribers WHERE token=?`, token,
	).Scan(&s.ID, &s.Email, &s.Token, &s.Monitors, &s.Confirmed, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

func (d *DB) ConfirmSubscriber(token string) error {
	_, err := d.db.Exec(`UPDATE subscribers SET confirmed=1 WHERE token=?`, token)
	return err
}

func (d *DB) DeleteSubscriberByToken(token string) error {
	_, err := d.db.Exec(`DELETE FROM subscribers WHERE token=?`, token)
	return err
}

func (d *DB) ListConfirmedSubscribers() ([]*Subscriber, error) {
	rows, err := d.db.Query(
		`SELECT id,email,token,monitors,confirmed,created_at FROM subscribers WHERE confirmed=1`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubscribers(rows)
}

func (d *DB) CountSubscribers() int {
	var n int
	d.db.QueryRow(`SELECT COUNT(*) FROM subscribers WHERE confirmed=1`).Scan(&n)
	return n
}

// ── Alert Channels ───────────────────────────────────────────────────────────

func (d *DB) CreateAlertChannel(c *AlertChannel) error {
	c.ID = newID()
	_, err := d.db.Exec(
		`INSERT INTO alert_channels (id,name,type,config) VALUES (?,?,?,?)`,
		c.ID, c.Name, c.Type, c.Config,
	)
	return err
}

func (d *DB) ListAlertChannels() ([]*AlertChannel, error) {
	rows, err := d.db.Query(`SELECT id,name,type,config FROM alert_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertChannels(rows)
}

func (d *DB) DeleteAlertChannel(id string) error {
	_, err := d.db.Exec(`DELETE FROM alert_channels WHERE id=?`, id)
	return err
}

// ── Heartbeat ────────────────────────────────────────────────────────────────

func (d *DB) RecordHeartbeat(monitorID string) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO heartbeats (monitor_id, last_ping) VALUES (?,?)`,
		monitorID, now(),
	)
	return err
}

func (d *DB) GetLastHeartbeat(monitorID string) int64 {
	var ts int64
	d.db.QueryRow(`SELECT last_ping FROM heartbeats WHERE monitor_id=?`, monitorID).Scan(&ts)
	return ts
}

// ── Scan helpers ─────────────────────────────────────────────────────────────

func scanMonitors(rows *sql.Rows) ([]*Monitor, error) {
	var ms []*Monitor
	for rows.Next() {
		m := &Monitor{}
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalS, &m.Regions, &m.Config, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	return ms, rows.Err()
}

func scanChecks(rows *sql.Rows) ([]*Check, error) {
	var cs []*Check
	for rows.Next() {
		c := &Check{}
		if err := rows.Scan(&c.ID, &c.MonitorID, &c.Region, &c.Status, &c.LatencyMs, &c.Detail, &c.CheckedAt); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

func scanIncidents(rows *sql.Rows) ([]*Incident, error) {
	var is []*Incident
	for rows.Next() {
		i := &Incident{}
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.Title, &i.Body, &i.Status, &i.StartedAt, &i.ResolvedAt); err != nil {
			return nil, err
		}
		is = append(is, i)
	}
	return is, rows.Err()
}

func scanSubscribers(rows *sql.Rows) ([]*Subscriber, error) {
	var ss []*Subscriber
	for rows.Next() {
		s := &Subscriber{}
		if err := rows.Scan(&s.ID, &s.Email, &s.Token, &s.Monitors, &s.Confirmed, &s.CreatedAt); err != nil {
			return nil, err
		}
		ss = append(ss, s)
	}
	return ss, rows.Err()
}

func scanAlertChannels(rows *sql.Rows) ([]*AlertChannel, error) {
	var cs []*AlertChannel
	for rows.Next() {
		c := &AlertChannel{}
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Config); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}
