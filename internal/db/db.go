package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if _, err := d.db.Exec(schema); err != nil {
		return err
	}
	// Idempotent column additions for existing databases.
	d.addColumnIfMissing("monitors", "group_name", "TEXT NOT NULL DEFAULT ''")
	d.addColumnIfMissing("monitors", "paused", "INTEGER NOT NULL DEFAULT 0")
	return nil
}

func (d *DB) addColumnIfMissing(table, col, definition string) {
	// SQLite will error with "duplicate column name" if it already exists — ignore that.
	_, err := d.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, definition))
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		// Log unexpected errors but don't fatal — migration continues.
		fmt.Printf("migrate alter %s.%s: %v\n", table, col, err)
	}
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
		`INSERT INTO monitors (id,name,type,target,interval_s,regions,config,status,group_name,paused,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Name, m.Type, m.Target, m.IntervalS, m.Regions, m.Config, m.Status,
		m.GroupName, btoi(m.Paused), m.CreatedAt,
	)
	return err
}

func (d *DB) GetMonitor(id string) (*Monitor, error) {
	m := &Monitor{}
	var paused int
	err := d.db.QueryRow(
		`SELECT id,name,type,target,interval_s,regions,config,status,group_name,paused,created_at
		 FROM monitors WHERE id=?`, id,
	).Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalS, &m.Regions, &m.Config, &m.Status,
		&m.GroupName, &paused, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	m.Paused = paused != 0
	return m, err
}

func (d *DB) ListMonitors() ([]*Monitor, error) {
	rows, err := d.db.Query(
		`SELECT id,name,type,target,interval_s,regions,config,status,group_name,paused,created_at
		 FROM monitors ORDER BY group_name ASC, created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMonitors(rows)
}

func (d *DB) UpdateMonitor(m *Monitor) error {
	_, err := d.db.Exec(
		`UPDATE monitors SET name=?,type=?,target=?,interval_s=?,regions=?,config=?,group_name=?,paused=? WHERE id=?`,
		m.Name, m.Type, m.Target, m.IntervalS, m.Regions, m.Config, m.GroupName, btoi(m.Paused), m.ID,
	)
	return err
}

func (d *DB) UpdateMonitorStatus(id, status string) error {
	_, err := d.db.Exec(`UPDATE monitors SET status=? WHERE id=?`, status, id)
	return err
}

func (d *DB) SetMonitorPaused(id string, paused bool) error {
	_, err := d.db.Exec(`UPDATE monitors SET paused=? WHERE id=?`, btoi(paused), id)
	return err
}

func (d *DB) DeleteMonitor(id string) error {
	_, err := d.db.Exec(`DELETE FROM monitors WHERE id=?`, id)
	return err
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
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

// GetLatencyHistory returns time-bucketed latency points for charting.
func (d *DB) GetLatencyHistory(monitorID string, hours int) ([]LatencyPoint, error) {
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	bucketSize := int64(3600) // 1-hour buckets
	if hours <= 24 {
		bucketSize = 900 // 15-min buckets for ≤24h
	}

	rows, err := d.db.Query(`
		SELECT
			(checked_at / ?) * ? AS bucket,
			AVG(latency_ms) AS avg_ms,
			COUNT(*) AS cnt
		FROM checks
		WHERE monitor_id=? AND checked_at >= ? AND latency_ms IS NOT NULL
		GROUP BY bucket
		ORDER BY bucket ASC`,
		bucketSize, bucketSize, monitorID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pts []LatencyPoint
	for rows.Next() {
		var p LatencyPoint
		if err := rows.Scan(&p.Bucket, &p.AvgMs, &p.CheckCount); err != nil {
			return nil, err
		}
		pts = append(pts, p)
	}
	return pts, rows.Err()
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

func (d *DB) ListIncidentsPaged(limit, offset int) ([]*Incident, int, error) {
	var total int
	d.db.QueryRow(`SELECT COUNT(*) FROM incidents`).Scan(&total)

	rows, err := d.db.Query(
		`SELECT id,monitor_id,title,body,status,started_at,resolved_at
		 FROM incidents ORDER BY started_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	incidents, err := scanIncidents(rows)
	return incidents, total, err
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

func (d *DB) GetAlertChannel(id string) (*AlertChannel, error) {
	c := &AlertChannel{}
	err := d.db.QueryRow(
		`SELECT id,name,type,config FROM alert_channels WHERE id=?`, id,
	).Scan(&c.ID, &c.Name, &c.Type, &c.Config)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
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

// ── Monitor–Channel routing ───────────────────────────────────────────────────

// GetChannelsForMonitor returns the alert channels assigned to a monitor.
// If none are assigned, returns all channels (backwards-compatible default).
func (d *DB) GetChannelsForMonitor(monitorID string) ([]*AlertChannel, error) {
	rows, err := d.db.Query(
		`SELECT ac.id,ac.name,ac.type,ac.config
		 FROM alert_channels ac
		 INNER JOIN monitor_channels mc ON mc.channel_id = ac.id
		 WHERE mc.monitor_id = ?
		 ORDER BY ac.name`,
		monitorID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels, err := scanAlertChannels(rows)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return d.ListAlertChannels()
	}
	return channels, nil
}

func (d *DB) SetMonitorChannels(monitorID string, channelIDs []string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM monitor_channels WHERE monitor_id=?`, monitorID); err != nil {
		return err
	}
	for _, cid := range channelIDs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO monitor_channels (monitor_id, channel_id) VALUES (?,?)`,
			monitorID, cid,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) GetMonitorChannelIDs(monitorID string) ([]string, error) {
	rows, err := d.db.Query(
		`SELECT channel_id FROM monitor_channels WHERE monitor_id=?`, monitorID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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

// ── Users ────────────────────────────────────────────────────────────────────

func (d *DB) CreateUser(u *User) error {
	u.ID = newID()
	u.CreatedAt = now()
	_, err := d.db.Exec(
		`INSERT INTO users (id,username,email,hashed_pass,role,created_at) VALUES (?,?,?,?,?,?)`,
		u.ID, u.Username, u.Email, u.HashedPass, u.Role, u.CreatedAt,
	)
	return err
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := d.db.QueryRow(
		`SELECT id,username,email,hashed_pass,role,created_at FROM users WHERE username=?`, username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.HashedPass, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) ListUsers() ([]*User, error) {
	rows, err := d.db.Query(
		`SELECT id,username,email,hashed_pass,role,created_at FROM users ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.HashedPass, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) DeleteUser(id string) error {
	_, err := d.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (d *DB) UpdateUserRole(id, role string) error {
	_, err := d.db.Exec(`UPDATE users SET role=? WHERE id=?`, role, id)
	return err
}

// ── API Keys ─────────────────────────────────────────────────────────────────

func (d *DB) CreateAPIKey(k *APIKey) error {
	k.ID = newID()
	k.CreatedAt = now()
	_, err := d.db.Exec(
		`INSERT INTO api_keys (id,name,key_hash,role,created_at) VALUES (?,?,?,?,?)`,
		k.ID, k.Name, k.KeyHash, k.Role, k.CreatedAt,
	)
	return err
}

func (d *DB) GetAPIKeyByHash(hash string) (*APIKey, error) {
	k := &APIKey{}
	err := d.db.QueryRow(
		`SELECT id,name,key_hash,role,created_at,last_used_at FROM api_keys WHERE key_hash=?`, hash,
	).Scan(&k.ID, &k.Name, &k.KeyHash, &k.Role, &k.CreatedAt, &k.LastUsedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

func (d *DB) ListAPIKeys() ([]*APIKey, error) {
	rows, err := d.db.Query(
		`SELECT id,name,key_hash,role,created_at,last_used_at FROM api_keys ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []*APIKey
	for rows.Next() {
		k := &APIKey{}
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Role, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (d *DB) DeleteAPIKey(id string) error {
	_, err := d.db.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	return err
}

func (d *DB) TouchAPIKey(id string) {
	d.db.Exec(`UPDATE api_keys SET last_used_at=? WHERE id=?`, now(), id)
}

// ── Maintenance Windows ───────────────────────────────────────────────────────

func (d *DB) CreateMaintenanceWindow(w *MaintenanceWindow) error {
	w.ID = newID()
	w.CreatedAt = now()
	_, err := d.db.Exec(
		`INSERT INTO maintenance_windows (id,label,monitor_id,starts_at,ends_at,created_at)
		 VALUES (?,?,?,?,?,?)`,
		w.ID, w.Label, w.MonitorID, w.StartsAt, w.EndsAt, w.CreatedAt,
	)
	return err
}

func (d *DB) ListMaintenanceWindows() ([]*MaintenanceWindow, error) {
	rows, err := d.db.Query(
		`SELECT id,label,monitor_id,starts_at,ends_at,created_at
		 FROM maintenance_windows ORDER BY starts_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ws []*MaintenanceWindow
	for rows.Next() {
		w := &MaintenanceWindow{}
		if err := rows.Scan(&w.ID, &w.Label, &w.MonitorID, &w.StartsAt, &w.EndsAt, &w.CreatedAt); err != nil {
			return nil, err
		}
		ws = append(ws, w)
	}
	return ws, rows.Err()
}

func (d *DB) DeleteMaintenanceWindow(id string) error {
	_, err := d.db.Exec(`DELETE FROM maintenance_windows WHERE id=?`, id)
	return err
}

// IsInMaintenance returns true if there is an active maintenance window covering
// this monitor (either a global window with no monitor_id, or one targeting it specifically).
func (d *DB) IsInMaintenance(monitorID string) bool {
	n := now()
	var count int
	d.db.QueryRow(
		`SELECT COUNT(*) FROM maintenance_windows
		 WHERE starts_at <= ? AND ends_at >= ?
		   AND ((monitor_id IS NULL OR monitor_id = '') OR monitor_id = ?)`,
		n, n, monitorID,
	).Scan(&count)
	return count > 0
}

// ── Audit Log ────────────────────────────────────────────────────────────────

func (d *DB) WriteAudit(actor, action, entityType, entityID, detail string) {
	d.db.Exec(
		`INSERT INTO audit_log (actor,action,entity_type,entity_id,detail,created_at)
		 VALUES (?,?,?,?,?,?)`,
		actor, action, entityType, entityID, detail, now(),
	)
}

func (d *DB) ListAuditLog(limit, offset int) ([]*AuditEntry, int, error) {
	var total int
	d.db.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&total)

	rows, err := d.db.Query(
		`SELECT id,actor,action,entity_type,entity_id,COALESCE(detail,''),created_at
		 FROM audit_log ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var entries []*AuditEntry
	for rows.Next() {
		e := &AuditEntry{}
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.EntityType, &e.EntityID, &e.Detail, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// ── Scan helpers ─────────────────────────────────────────────────────────────

func scanMonitors(rows *sql.Rows) ([]*Monitor, error) {
	var ms []*Monitor
	for rows.Next() {
		m := &Monitor{}
		var paused int
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.IntervalS, &m.Regions,
			&m.Config, &m.Status, &m.GroupName, &paused, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Paused = paused != 0
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
