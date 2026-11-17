# Argus

English | [Português](README.pt-BR.md)

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![Docker Pulls](https://img.shields.io/docker/pulls/winajr/argus?style=flat-square&color=blue)](https://hub.docker.com/r/winajr/argus)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)

Lightweight availability and health-check monitor for internal systems, servers, and cloud services, built in Go with a NOC-style interface, embedded SQLite storage, and real-time Server-Sent Events (SSE).

## Features

- **Cross-layer checks**: tests DNS/HTTP endpoints (configurable HTTP methods, custom headers, `expected_status`, `expected_body`, `json_path`, and `expected_dns`) alongside direct host reachability (ICMP ping, TCP port, or internal HTTP).
- **Root-cause diagnosis**: correlates check states to distinguish DNS failures, TLS certificate errors, Cloudflare edge errors (`502`, `520`–`526`), application outages, or full VM/network downtime.
- **SSL/TLS monitoring**: inspects HTTPS certificate expiration and flags `DEGRADED` ahead of expiry (`tls_warn_days`) and `DOWN` once expired.
- **Real-time updates via SSE**: instant card updates (`/events`), automatic severity-based sorting, 30-slot heartbeat bar, and latency sparkline.
- **Notifications**: outage and recovery alerts via generic Webhook, Slack, Discord, Telegram, and Email (SMTP), with cooldown control and maintenance suppression.
- **History & SLA**: embedded SQLite (WAL mode) with startup state hydration, 1h/24h/7d/30d timelines, MTBF/MTTR/P95 metrics, and CSV/JSON export.
- **Operations & observability**: dark/light themes, system groups, compact table view, TV mode (`?view=tv`), read-only public status page (`/status`), Prometheus metrics (`/metrics`), and `SIGHUP` hot config reload.

## Quick Start

### Docker Compose (Recommended)

```bash
git clone https://github.com/winstonajr/argus.git && cd argus
cp config.example.yaml config.yaml
cp .env.example .env
docker compose up -d
```

Open `http://localhost:8080`.

### Docker

```bash
docker run -d \
  --name argus \
  -p 8080:8080 \
  --cap-add=NET_RAW \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  winajr/argus:latest
```

### Running with Go

Prerequisites:
- Go 1.22+
- Raw socket capability for ICMP ping on Linux (optional if using HTTP/TCP checks only):
  ```bash
  sudo setcap cap_net_raw+ep ./bin/argus
  # or allow for the user group:
  sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"
  ```

Run:
```bash
go run ./cmd/argus -config config.example.yaml

# or build the binary:
make build
./bin/argus -config config.example.yaml
```

## Configuration

Configuration precedence: **Environment Variables > YAML File > Built-in Defaults**.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `ARGUS_SERVER_ADDR` | `:8080` | HTTP listen address and port (or `PORT=8080`) |
| `ARGUS_LANG` | `pt-BR` | UI, diagnosis, and alert language (`pt-BR` or `en`) |
| `ARGUS_SERVER_AUTH_USERNAME` | `admin` | Basic Auth username (active when password is set) |
| `ARGUS_SERVER_AUTH_PASSWORD` | — | Basic Auth password (protects UI/API; `/status`, `/healthz`, `/metrics` remain public) |
| `ARGUS_SERVER_AUTH_TOKEN` | — | Bearer / `X-Argus-Token` API token |
| `ARGUS_CONFIG_PATH` | `./config.yaml` | Path to YAML configuration file |
| `ARGUS_STORAGE_PATH` | `./data/argus.db` | SQLite database file path |
| `ARGUS_STORAGE_RETENTION_CHECKS` | `168h` | Retention window for raw check logs |
| `ARGUS_STORAGE_RETENTION_EVENTS` | `2160h` | Retention window for incident events |
| `ARGUS_DEFAULTS_INTERVAL` | `30s` | Default check interval |
| `ARGUS_DEFAULTS_HTTP_TIMEOUT` | `5s` | HTTP request timeout |
| `ARGUS_DEFAULTS_PING_TIMEOUT` | `3s` | ICMP / TCP ping timeout |
| `ARGUS_DEFAULTS_FAIL_THRESHOLD` | `2` | Consecutive failures to mark `DOWN` |
| `ARGUS_DEFAULTS_DEGRADED_LATENCY` | `2s` | Latency threshold for `DEGRADED` |
| `ARGUS_DEFAULTS_TLS_WARN_DAYS` | `15` | Days before TLS expiry to flag `DEGRADED` |
| `ARGUS_ALERTS_COOLDOWN` | `5m` | Minimum interval between repeated alerts |
| `ARGUS_ALERTS_NOTIFY_ON_DEGRADED` | `false` | Send notifications on `DEGRADED` transitions |
| `ARGUS_ALERTS_WEBHOOK_URL` | — | Generic JSON POST Webhook URL |
| `ARGUS_ALERTS_SLACK_WEBHOOK_URL` | — | Slack Incoming Webhook URL |
| `ARGUS_ALERTS_DISCORD_WEBHOOK_URL` | — | Discord Webhook URL |
| `ARGUS_ALERTS_TELEGRAM_BOT_TOKEN` | — | Telegram Bot Token |
| `ARGUS_ALERTS_TELEGRAM_CHAT_ID` | — | Telegram chat/group ID |
| `ARGUS_ALERTS_EMAIL_SMTP_HOST` | — | SMTP server host |
| `ARGUS_ALERTS_EMAIL_SMTP_PORT` | `587` | SMTP server port |
| `ARGUS_ALERTS_EMAIL_USERNAME` | — | SMTP username |
| `ARGUS_ALERTS_EMAIL_PASSWORD` | — | SMTP password |
| `ARGUS_ALERTS_EMAIL_FROM` | — | Alert sender email address |
| `ARGUS_ALERTS_EMAIL_TO` | — | Comma-separated recipient email addresses |

### Variable Interpolation in `config.yaml`

The YAML file supports `${VAR}` and `${VAR:-fallback}` syntax:

```yaml
server:
  addr: "${PORT:-:8080}"

storage:
  path: "${ARGUS_STORAGE_PATH:-./data/argus.db}"

systems:
  - id: "erp"
    name: "Corporate ERP"
    group: "Production"
    url: "${ERP_URL:-https://erp.company.local/health}"
    ip: "${ERP_INTERNAL_IP:-10.0.0.15}"
```

### Hot Reload

Reload `config.yaml` without restarting the server or losing in-memory history:

```bash
docker kill -s HUP argus
# or locally:
kill -HUP $(pidof argus)
```

## Endpoints

| Route | Description |
|---|---|
| `GET /` | NOC operations dashboard |
| `GET /status` | Public read-only status page (hides internal IPs) |
| `GET /systems/{id}/history` | History, timeline, and SLA metrics (1h, 24h, 7d, 30d) |
| `GET /systems/{id}/export` | Export history (`?period=30d&format=csv` or `json`) |
| `GET /api/systems` | List all systems in JSON |
| `GET /api/systems/{id}` | Single system details in JSON |
| `GET /api/events` | Recent status change events |
| `POST /api/systems/{id}/maintenance` | Toggle maintenance mode at runtime |
| `GET /metrics` | Prometheus metrics |
| `GET /healthz` | Application and SQLite healthcheck |

## Keyboard Shortcuts

- `/` : focus search input.
- `Esc` : clear search and blur.
- `1` : filter all systems.
- `2` : filter `DOWN` systems.
- `3` : filter `DEGRADED` systems.
- `4` : filter `UP` systems.
- `t` : toggle dark/light theme.
- `v` : toggle TV mode.

## Development and Testing

```bash
go test -v -race ./...
go vet ./...
```

## Contributing

This project follows **GitFlow** and [Conventional Commits](https://www.conventionalcommits.org/):

- Primary branches: `main` (releases) and `develop` (integration).
- Working branches: `feature/*`, `fix/*`, `docs/*`, `ci/*`, `chore/*`.
- Commit format: `<type>(scope): short imperative description` (e.g., `feat(notify): add slack webhook`, `fix(web): remove click interception on cards`).

## License

Distributed under the **MIT** License. See [LICENSE](LICENSE) for more information.
