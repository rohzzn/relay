# Relay

**Self-hosted uptime monitoring with a built-in public status page.**

Uptime Kuma monitors your services; Relay monitors them *and* tells your users about it — one tool, zero duct tape.

```bash
docker run -d -p 8080:8080 -v relay-data:/data \
  -e RELAY_SECRET=$(openssl rand -hex 16) \
  -e RELAY_ADMIN_PASS=changeme \
  ghcr.io/relay-monitor/relay
```

Then open `http://localhost:8080` for the status page and `http://localhost:8080/admin` for the dashboard.

---

## Why Relay?

|                   | Uptime Kuma   | Statuspage.io  | **Relay**      |
|-------------------|:---:|:---:|:---:|
| Self-hosted       | ✓   | ✗              | ✓              |
| Public status page| ✗   | ✓              | ✓              |
| Email subscribers | ✗   | ✓ (paid)       | ✓              |
| Multi-region      | ✗   | ✓ (paid)       | ✓ (v2)         |
| Binary size       | ~200 MB (Node) | —             | **~15 MB**     |
| Database          | SQLite         | —             | SQLite         |
| Free              | ✓   | ✗              | ✓              |

---

## Features

- **HTTP, TCP, TLS, and Heartbeat monitors** — check APIs, ports, cert expiry, and cron jobs
- **Live dashboard** — real-time status updates via WebSocket, no polling
- **90-day uptime bars** — visual history for every monitor
- **Incident management** — post and update incidents, visible on the status page
- **Email subscribers** — double-opt-in, unsubscribe links
- **Alert channels** — Slack, webhook, email (SMTP)
- **Single Go binary** — no Node, no Python, no runtime dependencies
- **SQLite WAL mode** — zero-ops database, backup with `cp`
- **Distroless Docker image** — under 20 MB

---

## Quick start

### Docker (recommended)

```bash
# Copy the example env file
cp .env.example .env
# Edit .env and set RELAY_SECRET + RELAY_ADMIN_PASS

docker compose up -d
```

Your status page is at `http://localhost:8080`.  
Admin dashboard is at `http://localhost:8080/admin`.

### With Caddy (auto-TLS)

```bash
# Edit Caddyfile — replace status.example.com with your domain
docker compose up -d
```

Caddy handles Let's Encrypt automatically.

### Build from source

```bash
git clone https://github.com/relay-monitor/relay
cd relay
go build -o relay ./cmd/relay

RELAY_SECRET=secret RELAY_ADMIN_PASS=admin ./relay
```

---

## Configuration

All configuration is via environment variables (see `.env.example`):

| Variable | Default | Description |
|---|---|---|
| `RELAY_SECRET` | — | **Required.** HMAC secret for session cookies |
| `RELAY_ADMIN_USER` | `admin` | Admin username |
| `RELAY_ADMIN_PASS` | — | **Required.** Admin password |
| `RELAY_SITE_NAME` | `Status` | Site name shown on the status page |
| `RELAY_SITE_URL` | `http://localhost:8080` | Public URL of your status page |
| `RELAY_SMTP_HOST` | — | SMTP server for email alerts |
| `RELAY_DATA` | `./data` | Directory for SQLite database |
| `RELAY_PORT` | `8080` | HTTP listen port |
| `RELAY_CHECK_CONCURRENCY` | `20` | Max concurrent monitor checks |
| `RELAY_RETENTION_DAYS` | `90` | Days of check history to keep |

---

## Monitor types

| Type | Target format | Example |
|---|---|---|
| **http** | URL | `https://api.example.com/health` |
| **tcp** | `host:port` | `db.example.com:5432` |
| **tls** | hostname or `host:port` | `api.example.com` |
| **heartbeat** | (auto — the monitor's own ID) | — |

For **heartbeat** monitors, your cron job must POST to:
```
POST /ping/{monitor-id}
```

Example cron:
```bash
# Every 5 minutes — notify Relay
*/5 * * * * curl -s -X POST https://status.example.com/ping/your-monitor-id
```

---

## Alert channels

Configure alert channels in the admin dashboard → **Alert Channels**:

- **Webhook** — POST JSON to any URL
- **Slack** — Slack incoming webhook
- **Email** — SMTP

---

## Architecture

```
relay/
├── cmd/relay/          Entry point — wires dependencies, starts server
├── internal/
│   ├── config/         Environment-based configuration
│   ├── db/             SQLite queries (no ORM)
│   ├── check/          HTTP, TCP, TLS, Heartbeat check implementations
│   ├── scheduler/      Per-monitor goroutines with configurable intervals
│   ├── state/          Up/down/degraded FSM, auto incident open/close
│   ├── alert/          Channel dispatch with 10-minute cooldown
│   ├── notify/         Slack, SMTP, webhook adapters
│   └── server/         HTTP handlers, WebSocket hub, auth
└── web/
    ├── templates/       Go html/template pages (embedded in binary)
    └── static/          CSS + JS (embedded in binary)
```

**Tech choices:**
- **Go** — single binary, ~15 MB Docker image, goroutine-per-monitor scheduler
- **SQLite WAL** — zero-ops, backup with `cp`, handles dozens of concurrent readers
- **HTMX + Alpine.js** — real-time dashboard without React, no build step
- **Distroless image** — no shell in production, minimal attack surface

---

## Roadmap

- [x] HTTP, TCP, TLS, Heartbeat monitors
- [x] Live dashboard (WebSocket)
- [x] 90-day uptime bars
- [x] Incident management
- [x] Slack / webhook / email alert channels
- [x] Email subscriber list
- [ ] **v2:** Multi-region probe agent (`relay-probe` binary)
- [ ] **v2:** Maintenance windows
- [ ] **v2:** API access
- [ ] **v2:** Response time history graphs
- [ ] **v3:** On-call schedule with rotating recipients
- [ ] **v3:** SSO (OIDC)

---

## License

MIT
