<p align="center">
  <img src="web/static/favicon.svg" alt="Relay" width="72" height="72">
</p>

<h1 align="center">Relay</h1>

<p align="center">Self-hosted uptime monitoring <strong>with a built-in public status page</strong> — one tool, zero duct tape.</p>

![License](https://img.shields.io/badge/license-MIT-blue)
![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)
![Docker Image Size](https://img.shields.io/badge/docker%20image-%3C20MB-brightgreen)

Uptime Kuma alerts *you* when something breaks. Relay alerts you **and your users** — through a beautiful, self-hosted status page with incident management, email subscribers, and Slack/webhook alerts. All in a single ~15 MB Go binary with a SQLite database.

```bash
docker run -d -p 8080:8080 -v relay-data:/data \
  -e RELAY_SECRET=$(openssl rand -hex 16) \
  -e RELAY_ADMIN_PASS=yourpassword \
  ghcr.io/rohzzn/relay
```

---

<!-- Replace with a real screenshot before publishing -->
> **Screenshot:** *Status page + admin dashboard screenshots go here*

---

## Why Relay?

Self-hosters need two separate tools to do what Relay does in one:

|                    | Uptime Kuma     | Statuspage.io   | **Relay**       |
|--------------------|:---------------:|:---------------:|:---------------:|
| Self-hosted        | ✓               | ✗               | ✓               |
| Public status page | ✗               | ✓               | ✓               |
| Email subscribers  | ✗               | ✓ (paid)        | ✓               |
| Incident management| ✗               | ✓               | ✓               |
| Multi-region probes| ✗               | ✓ (paid)        | ✓ (v2)          |
| Slack / webhook    | ✓               | ✓ (paid)        | ✓               |
| Docker image size  | ~200 MB (Node)  | —               | **< 20 MB**     |
| Database           | SQLite          | —               | SQLite          |
| Build step         | Required        | —               | None            |
| Free               | ✓               | ✗               | ✓               |

---

## Features

- **HTTP, TCP, TLS, DNS, and Heartbeat monitors** — check APIs, ports, certificate expiry, DNS resolution, and cron jobs
- **Live admin dashboard** — real-time status updates via WebSocket, no page reload needed
- **90-day uptime bars** — visual history per monitor, identical to Statuspage.io
- **Public status page** — fast, works without JavaScript, bookmark-worthy
- **Incident management** — post incidents, add timeline updates, mark resolved
- **Email subscribers** — double opt-in, one-click unsubscribe
- **Alert channels** — Slack, generic webhook, email (SMTP)
- **Single Go binary** — no Node, no Python, no runtime dependencies
- **SQLite WAL mode** — zero ops, back up with `cp`
- **Distroless Docker image** — under 20 MB, no shell in production

---

## Quick Start

### One-liner (no config file)

```bash
docker run -d \
  --name relay \
  --restart unless-stopped \
  -p 8080:8080 \
  -v relay-data:/data \
  -e RELAY_SECRET=$(openssl rand -hex 16) \
  -e RELAY_SITE_NAME="Acme Status" \
  -e RELAY_SITE_URL="https://status.example.com" \
  -e RELAY_ADMIN_PASS=yourpassword \
  ghcr.io/rohzzn/relay
```

- **Status page:** `http://localhost:8080`
- **Admin dashboard:** `http://localhost:8080/admin` (login: `admin` / your password)

### Docker Compose + Caddy (auto-TLS, recommended)

```bash
git clone https://github.com/rohzzn/relay
cd relay
cp .env.example .env
# Edit .env — set RELAY_SECRET, RELAY_ADMIN_PASS, RELAY_SITE_NAME, RELAY_SITE_URL
# Edit Caddyfile — replace status.example.com with your domain

docker compose up -d
```

Caddy handles Let's Encrypt automatically. No certificate configuration needed.

### Build from source

```bash
git clone https://github.com/rohzzn/relay
cd relay
go build -o relay ./cmd/relay

RELAY_SECRET=secret RELAY_ADMIN_PASS=admin ./relay
```

Requires Go 1.22+. No other dependencies.

---

## Configuration

All settings are environment variables. Only two are required:

| Variable | Required | Default | Description |
|---|:---:|---|---|
| `RELAY_SECRET` | **Yes** | — | HMAC key for session cookies. Generate with `openssl rand -hex 16` |
| `RELAY_ADMIN_PASS` | **Yes** | — | Admin dashboard password |
| `RELAY_ADMIN_USER` | | `admin` | Admin username |
| `RELAY_SITE_NAME` | | `Status` | Displayed on the public status page |
| `RELAY_SITE_URL` | | `http://localhost:8080` | Full public URL (used in email links) |
| `RELAY_LOGO_URL` | | — | Optional logo image URL for the status page |
| `RELAY_SMTP_HOST` | | — | SMTP server hostname |
| `RELAY_SMTP_PORT` | | `587` | SMTP port |
| `RELAY_SMTP_USER` | | — | SMTP username |
| `RELAY_SMTP_PASS` | | — | SMTP password |
| `RELAY_SMTP_FROM` | | — | From address for outgoing emails |
| `RELAY_DATA` | | `./data` | Directory for `relay.db` |
| `RELAY_PORT` | | `8080` | HTTP listen port |
| `RELAY_CHECK_CONCURRENCY` | | `20` | Max concurrent check goroutines |
| `RELAY_RETENTION_DAYS` | | `90` | Days of check history to keep |

> **No SMTP?** Subscriber confirmations are auto-approved so the feature still works during local testing.

---

## Monitor Types

| Type | Target | Example |
|---|---|---|
| **http** | URL | `https://api.example.com/health` |
| **tcp** | `host:port` | `db.internal:5432` |
| **tls** | hostname | `api.example.com` |
| **dns** | hostname | `example.com` |
| **heartbeat** | *(auto)* | cron jobs POST to Relay |

### Heartbeat monitors

A heartbeat monitor expects your cron job to POST to Relay on each run. If it stops, Relay opens an incident.

After creating a heartbeat monitor, the dashboard shows the exact endpoint:

```
POST https://status.example.com/ping/{monitor-id}
```

Example crontab entry:
```cron
*/5 * * * * /path/to/job && curl -sS -X POST https://status.example.com/ping/YOUR_MONITOR_ID
```

---

## Alert Channels

Configure channels in **Admin → Alert Channels**:

| Type | Config |
|---|---|
| **Webhook** | Any URL — Relay POSTs JSON on down/up events |
| **Slack** | Slack incoming webhook URL |
| **Email** | SMTP credentials + recipient address |

Alerts have a built-in **10-minute cooldown** per monitor to prevent alert storms. Recovery ("back up") notifications always send immediately.

---

## Architecture

```
relay/
├── cmd/relay/          Entry point, graceful shutdown, healthcheck subcommand
├── internal/
│   ├── config/         Environment-based configuration
│   ├── db/             SQLite (WAL mode), all queries — no ORM
│   ├── check/          HTTP · TCP · TLS · DNS · Heartbeat
│   ├── scheduler/      Per-monitor goroutines with semaphore concurrency pool
│   ├── state/          FSM: up/degraded/down, auto incident open/close
│   ├── alert/          Cooldown-aware channel dispatcher
│   ├── notify/         Slack, SMTP, and webhook adapters
│   └── server/         HTTP handlers, WebSocket hub, HMAC session auth
└── web/
    ├── templates/       html/template pages — embedded in the binary
    └── static/          CSS + minimal JS — embedded in the binary
```

**Why Go?** Single static binary, goroutine-per-monitor scheduler, ~15 MB Docker image. Uptime Kuma is 200 MB and people notice.

**Why SQLite WAL?** Zero ops. Back up with `cp relay.db relay.db.bak`. Handles dozens of concurrent readers. No Postgres to run.

**Why HTMX?** The live dashboard has real-time updates with zero client-side framework. No React, no build step, loads in <300ms on a Raspberry Pi.

---

## Roadmap

- [x] HTTP, TCP, TLS, DNS, Heartbeat monitors
- [x] Live dashboard via WebSocket
- [x] 90-day uptime bars
- [x] Incident management with timeline updates
- [x] Email subscriber list with confirmation
- [x] Slack, webhook, and SMTP alert channels
- [x] Custom 404/500 pages
- [x] Docker healthcheck subcommand
- [ ] **v2:** `relay-probe` — lightweight agent binary for multi-region monitoring
- [ ] **v2:** Maintenance windows (suppress alerts during deployments)
- [ ] **v2:** Response time history graphs
- [ ] **v2:** REST API
- [ ] **v3:** On-call schedule with rotating recipients
- [ ] **v3:** SSO (OIDC)

---

## Contributing

1. Fork the repo
2. `go run ./cmd/relay` — starts with live template reloading
3. Templates are in `web/templates/`, styles in `web/static/style.css`
4. Open a PR — all contributions welcome

**Code style:** Standard `gofmt`. No ORM — queries live in `internal/db/db.go` as plain SQL. No external router — Go 1.22 stdlib mux only.

---

## License

MIT — see [LICENSE](LICENSE)

---

*Relay is not affiliated with Uptime Kuma or Statuspage.io.*
