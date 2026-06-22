package db

const schema = `
CREATE TABLE IF NOT EXISTS monitors (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	type        TEXT NOT NULL,
	target      TEXT NOT NULL,
	interval_s  INTEGER NOT NULL DEFAULT 60,
	regions     TEXT NOT NULL DEFAULT 'local',
	config      TEXT NOT NULL DEFAULT '{}',
	status      TEXT NOT NULL DEFAULT 'pending',
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS checks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id  TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	region      TEXT NOT NULL DEFAULT 'local',
	status      TEXT NOT NULL,
	latency_ms  INTEGER,
	detail      TEXT,
	checked_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checks_monitor ON checks(monitor_id, checked_at DESC);

CREATE TABLE IF NOT EXISTS incidents (
	id          TEXT PRIMARY KEY,
	monitor_id  TEXT REFERENCES monitors(id) ON DELETE SET NULL,
	title       TEXT NOT NULL,
	body        TEXT,
	status      TEXT NOT NULL DEFAULT 'investigating',
	started_at  INTEGER NOT NULL,
	resolved_at INTEGER
);

CREATE TABLE IF NOT EXISTS subscribers (
	id          TEXT PRIMARY KEY,
	email       TEXT NOT NULL UNIQUE,
	token       TEXT NOT NULL UNIQUE,
	monitors    TEXT,
	confirmed   INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS alert_channels (
	id      TEXT PRIMARY KEY,
	name    TEXT NOT NULL,
	type    TEXT NOT NULL,
	config  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS heartbeats (
	monitor_id  TEXT PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
	last_ping   INTEGER NOT NULL
);
`
