# Argus

[![Docker Pulls](https://img.shields.io/docker/pulls/winajr/argus?style=flat-square&color=blue)](https://hub.docker.com/r/winajr/argus)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![Architectures](https://img.shields.io/badge/arch-amd64%20%7C%20arm64-blueviolet?style=flat-square)](https://hub.docker.com/r/winajr/argus)
[![GitHub](https://img.shields.io/badge/GitHub-winstonajr%2Fargus-181717?style=flat-square&logo=github)](https://github.com/winstonajr/argus)

**Argus** is a lightweight, zero-dependency, modern availability and health-check monitoring dashboard designed for internal systems, servers, and cloud services.

Featuring an operation-room (NOC) interface with custom color themes and automatic seasonal holiday themes, real-time Server-Sent Events (SSE) updates, embedded SQLite storage, configurable IANA timezone (`ARGUS_TIMEZONE`), SSL/TLS expiration tracking, rich multi-channel alerting, optional authentication, bilingual UI (`pt-BR` / `en`), and automatic root-cause correlation between DNS/HTTP endpoints and raw host connectivity (ICMP/TCP).

---

## Quick Start

### Option 1: Docker Run

Run directly with raw network capabilities for ICMP ping:

```bash
docker run -d \
  --name argus \
  -p 8080:8080 \
  --cap-add=NET_RAW \
  -e ARGUS_TIMEZONE=America/Sao_Paulo \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  winajr/argus:latest
```

Open your browser at **http://localhost:8080**.

---

### Option 2: Docker Compose (Recommended)

Save the following as `docker-compose.yml`:

```yaml
services:
  argus:
    image: winajr/argus:latest
    container_name: argus
    restart: unless-stopped
    ports:
      - "${PORT:-8080}:8080"
    cap_add:
      - NET_RAW # Required for ICMP ping socket
    environment:
      - ARGUS_CONFIG_PATH=/app/config.yaml
      - ARGUS_STORAGE_PATH=/app/data/argus.db
      - ARGUS_LANG=${ARGUS_LANG:-pt-BR} # "pt-BR" or "en"
      - ARGUS_TIMEZONE=${ARGUS_TIMEZONE:-America/Sao_Paulo} # IANA timezone (e.g. America/Sao_Paulo, UTC)
      - ARGUS_SEASONAL_THEMES=${ARGUS_SEASONAL_THEMES:-true} # Automatic seasonal themes on holidays
      # Optional: Santa sleigh (9 reindeer) & snow easter egg when all systems are DOWN
      # - ARGUS_SANTA_EASTER_EGG=false
      # Optional authentication (protects dashboard & API while keeping /status, /healthz, /metrics public)
      # - ARGUS_SERVER_AUTH_USERNAME=admin
      # - ARGUS_SERVER_AUTH_PASSWORD=secret
      # - ARGUS_SERVER_AUTH_TOKEN=my-api-token
      # Optional: configure alerts directly via environment variables
      # - ARGUS_DEFAULTS_TLS_WARN_DAYS=15
      # - ARGUS_ALERTS_COOLDOWN=5m
      # - ARGUS_ALERTS_NOTIFY_ON_DEGRADED=false
      # - ARGUS_PUBLIC_URL=https://status.example.com
      # - ARGUS_ALERTS_WEBHOOK_URL=https://hooks.example.com/argus
      # - ARGUS_ALERTS_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
      # - ARGUS_ALERTS_DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/...
      # - ARGUS_ALERTS_TELEGRAM_BOT_TOKEN=123456:ABC-DEF
      # - ARGUS_ALERTS_TELEGRAM_CHAT_ID=-1001234567890
      # - ARGUS_ALERTS_EMAIL_SMTP_HOST=smtp.example.com
      # - ARGUS_ALERTS_EMAIL_SMTP_PORT=587 # 587 (STARTTLS) or 465 (implicit TLS)
      # - ARGUS_ALERTS_EMAIL_USERNAME=argus@example.com
      # - ARGUS_ALERTS_EMAIL_PASSWORD=secret
      # - ARGUS_ALERTS_EMAIL_FROM=argus@example.com
      # - ARGUS_ALERTS_EMAIL_TO=ops@example.com,sre@example.com
    env_file:
      - path: .env
        required: false
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - ./data:/app/data
```

Launch the stack:
```bash
docker compose up -d
```

---

## Key Features

- **Cross-Layer Diagnostics**: Checks both DNS/HTTP applications (including expected DNS IP, custom HTTP methods/headers, body substring or `/regex/`, and JSON path assertions) and host reachability (ICMP/TCP ping or internal HTTP). Immediately pinpoints whether an outage is a DNS failure, TLS error, unexpected HTTP status, Cloudflare edge error (`502`–`526`), application crash (host alive), or complete VM/network outage.
- **Real-Time SSE Updates & Telemetry**: Instant, per-language card updates via Server-Sent Events (`/events`) with priority sorting (`DOWN > DEGRADED > UP`) or manual drag-and-drop card ordering, on-demand checks, 30-slot heartbeat bar, and latency sparkline directly on every card.
- **SSL/TLS Expiration Monitoring**: Automatically inspects TLS certificates on HTTPS targets, flags `DEGRADED` before expiration (`tls_warn_days`), and `DOWN` when expired.
- **Rich Multi-Channel Notifications**: Formatted alerts for outages, degraded states, and recoveries (with root-cause diagnosis, layer/target metadata, MTTR downtime duration, and optional `ARGUS_PUBLIC_URL` dashboard CTA) via Telegram (HTML), Email (multipart HTML/plain via SMTP STARTTLS on `587` or implicit TLS on `465`), Slack attachments, Discord embeds, and Generic JSON Webhook (with custom headers), plus one-click test alerts, anti-flapping cooldown, and maintenance suppression.
- **Self-Contained Storage & SLA History**: Embedded pure-Go SQLite (WAL mode) with startup state hydration, persisted timed maintenance windows, clickable external target links on history pages, 1h/24h/7d/30d timeline views, paginated failed-check logs, MTBF/MTTR/P95 metrics, and formula-safe CSV/JSON export.
- **Themes, NOC / TV Mode, i18n & Public Status**: Custom color themes (`auto`, `dark`, `light`, `midnight`, `nord`, `warm`, `emerald`, `latte`), automatic Brazilian & international seasonal holiday themes (`ARGUS_SEASONAL_THEMES`), bilingual UI (`pt-BR` / `en`), configurable IANA timezone (`server.timezone` / `ARGUS_TIMEZONE`), adjustable card scaling (`+` / `-` / `0`, UI controls, or `?size=lg|xl|xxl`) to fill widescreen and NOC walls, system groups, compact table view, optional sound alerts, high-density TV heatmap tiles (`?view=tv` with quick `ESC` / `V` or button exit), read-only public status page (`/status`), Prometheus metrics (`/metrics`), `SIGHUP` hot config reload, and an optional Santa sleigh & snow easter egg when all systems are down.
- **Optional Authentication & Hardening**: Optional HTTP Basic Auth and Bearer / `X-Argus-Token` API authentication (keeping `/status`, `/healthz`, and `/metrics` public), strict CSP, and gzip/ETag static caching.
- **Environment & Variable Interpolation**: Native support for `.env` files, `${VAR:-default}` placeholders in YAML, and `ARGUS_*` overrides.

---

## Configuration (`config.yaml`)

Example configuration file:

```yaml
server:
  addr: ":8080"
  lang: "pt-BR" # "pt-BR" or "en"
  timezone: "America/Sao_Paulo" # IANA timezone (e.g. "America/Sao_Paulo", "UTC")
  seasonal_themes: true # Automatic holiday themes when UI theme is set to auto
  santa_easter_egg: false # Set true for snow + Santa sleigh when all systems are DOWN
  # auth:
  #   username: "${ARGUS_SERVER_AUTH_USERNAME:-admin}"
  #   password: "${ARGUS_SERVER_AUTH_PASSWORD:-}"
  #   token: "${ARGUS_SERVER_AUTH_TOKEN:-}"

storage:
  path: "./data/argus.db"
  retention_checks: 168h   # 7 days of raw check logs
  retention_events: 2160h  # 90 days of incident timeline

defaults:
  interval: 30s
  http_timeout: 5s
  ping_timeout: 3s
  fail_threshold: 2
  degraded_latency: 2s
  tls_warn_days: 15

alerts:
  cooldown: 5m
  notify_on_degraded: false
  # public_url: "${ARGUS_PUBLIC_URL:-}"
  # slack:
  #   webhook_url: "${ARGUS_ALERTS_SLACK_WEBHOOK_URL:-}"
  # discord:
  #   webhook_url: "${ARGUS_ALERTS_DISCORD_WEBHOOK_URL:-}"

systems:
  - id: "erp"
    name: "Corporate ERP"
    group: "Production"
    url: "https://erp.internal.local/health"
    # method: "GET"
    # headers:
    #   Authorization: "Bearer ${ERP_TOKEN:-}"
    # expected_body: "\"status\":\"ok\""
    # json_path: "$.status == 'ok'"
    # expected_dns: ["10.0.0.15"]
    ip: "10.0.0.15"

  - id: "api-gateway"
    name: "API Gateway"
    group: "Infrastructure"
    url: "https://api.internal.local"
    ip: "10.0.0.22"
    tcp_port: 443
```

Reload configuration without restarting the container:
```bash
docker kill -s HUP argus
```

---

## Endpoints

| Endpoint | Description |
|---|---|
| `GET /` | NOC Operations Dashboard (add `?view=tv` for TV heatmap mode, `?size=lg\|xl\|xxl` for larger cards) |
| `GET /events` | Real-time Server-Sent Events (SSE) stream |
| `GET /status` | Public read-only status page (hides internal IPs) |
| `GET /systems/{id}/history` | Diagnosis, timeline, incidents, MTBF/MTTR/P95 & SLA |
| `GET /systems/{id}/export?period=30d&format=csv` | Export history (`csv` or `json`) |
| `GET /api/systems` & `GET /api/systems/{id}` | JSON system status API |
| `GET /api/systems/{id}/failed-checks` | Paginated failed check logs (`?period=24h&offset=0`) |
| `POST /api/systems/{id}/check` | Trigger an immediate on-demand check |
| `POST /api/systems/{id}/maintenance` | Toggle or schedule maintenance mode (`?enabled=true&duration=1h`) |
| `POST /api/alerts/test` | Send a test notification to all configured channels |
| `GET /api/events` | Recent status change events |
| `GET /metrics` | Prometheus metrics |
| `GET /healthz` | Healthcheck endpoint |

---

## Environment Variables

Argus supports configuration overrides via environment variables:

| Variable | Default | Description |
|---|---|---|
| `PORT` or `ARGUS_SERVER_ADDR` | `:8080` | HTTP listening port/address |
| `ARGUS_LANG` | `pt-BR` | UI, diagnosis, and alert language (`pt-BR` or `en`) |
| `ARGUS_TIMEZONE` | System local (`TZ`) | IANA timezone for displayed timestamps (e.g. `America/Sao_Paulo`, `UTC`; YAML: `server.timezone`) |
| `ARGUS_SEASONAL_THEMES` | `true` | Automatic holiday/seasonal themes when theme choice is `auto` (YAML: `server.seasonal_themes`) |
| `ARGUS_SANTA_EASTER_EGG` | `false` | Enables snow and Santa sleigh with 9 reindeer when all systems are `DOWN` (YAML: `server.santa_easter_egg`) |
| `ARGUS_SERVER_AUTH_USERNAME` | `admin` | Basic Auth username (active when password is set) |
| `ARGUS_SERVER_AUTH_PASSWORD` | — | Basic Auth password (protects dashboard & API) |
| `ARGUS_SERVER_AUTH_TOKEN` | — | Bearer / `X-Argus-Token` API authentication token |
| `ARGUS_CONFIG_PATH` | `./config.yaml` | Path to YAML configuration file |
| `ARGUS_STORAGE_PATH` | `./data/argus.db` | SQLite database destination |
| `ARGUS_STORAGE_RETENTION_CHECKS` | `168h` | Check logs retention duration |
| `ARGUS_STORAGE_RETENTION_EVENTS` | `2160h` | Incident history retention duration |
| `ARGUS_DEFAULTS_INTERVAL` | `30s` | Default polling interval |
| `ARGUS_DEFAULTS_HTTP_TIMEOUT` | `5s` | Timeout for HTTP checks |
| `ARGUS_DEFAULTS_PING_TIMEOUT` | `3s` | Timeout for ICMP/TCP pings |
| `ARGUS_DEFAULTS_FAIL_THRESHOLD` | `2` | Consecutive failures before marking DOWN |
| `ARGUS_DEFAULTS_DEGRADED_LATENCY` | `2s` | Latency threshold for DEGRADED |
| `ARGUS_DEFAULTS_TLS_WARN_DAYS` | `15` | Days before TLS expiry to flag DEGRADED |
| `ARGUS_ALERTS_COOLDOWN` | `5m` | Minimum interval between repeated alerts |
| `ARGUS_ALERTS_NOTIFY_ON_DEGRADED` | `false` | Send alerts on `DEGRADED` transitions |
| `ARGUS_PUBLIC_URL` | — | External dashboard URL included as a link/CTA in alerts (YAML: `alerts.public_url`) |
| `ARGUS_ALERTS_WEBHOOK_URL` | — | Generic JSON Webhook URL |
| `ARGUS_ALERTS_SLACK_WEBHOOK_URL` | — | Slack Incoming Webhook URL |
| `ARGUS_ALERTS_DISCORD_WEBHOOK_URL` | — | Discord Webhook URL |
| `ARGUS_ALERTS_TELEGRAM_BOT_TOKEN` | — | Telegram Bot Token |
| `ARGUS_ALERTS_TELEGRAM_CHAT_ID` | — | Telegram Chat ID |
| `ARGUS_ALERTS_EMAIL_SMTP_HOST` | — | SMTP server host |
| `ARGUS_ALERTS_EMAIL_SMTP_PORT` | `587` | SMTP server port (`587` STARTTLS, `465` implicit TLS) |
| `ARGUS_ALERTS_EMAIL_USERNAME` | — | SMTP username |
| `ARGUS_ALERTS_EMAIL_PASSWORD` | — | SMTP password |
| `ARGUS_ALERTS_EMAIL_FROM` | — | Alert sender email address |
| `ARGUS_ALERTS_EMAIL_TO` | — | Comma-separated recipient emails |

---

## Repository & Documentation

Source code, issue tracker, and contribution guide:
[github.com/winstonajr/argus](https://github.com/winstonajr/argus)
