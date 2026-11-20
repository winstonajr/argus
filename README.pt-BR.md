# Argus

[English](README.md) | Português

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![Docker Pulls](https://img.shields.io/docker/pulls/winajr/argus?style=flat-square&color=blue)](https://hub.docker.com/r/winajr/argus)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)

Monitor de disponibilidade e saúde para sistemas internos, servidores e serviços em nuvem, construído em Go com interface estilo NOC, armazenamento SQLite embutido e atualizações em tempo real via Server-Sent Events (SSE).

## Recursos

- **Verificação em duas camadas**: testa endpoints DNS/HTTP (métodos configuráveis, headers customizados, `expected_status`, `expected_body`, `json_path` e `expected_dns`) e conectividade direta no host (ICMP ping, porta TCP ou HTTP interno).
- **Diagnóstico automático**: correlaciona os resultados para distinguir falhas de DNS, erros de certificado TLS, borda Cloudflare (`502`, `520`–`526`), indisponibilidade da aplicação ou queda de VM/rede.
- **Monitoramento SSL/TLS**: verifica a validade de certificados HTTPS e sinaliza `DEGRADED` antes do vencimento (`tls_warn_days`) e `DOWN` após expirar.
- **Tempo real via SSE**: atualização instantânea dos cards (`/events`), reordenação automática por severidade, barra de 30 heartbeats e sparkline de latência.
- **Notificações**: envio de alertas de queda e recuperação via Webhook genérico, Slack, Discord, Telegram e E-mail (SMTP), com controle de *cooldown* e supressão em modo de manutenção.
- **Histórico e SLA**: persistência em SQLite (WAL) com hidratação no restart, linha do tempo (1h, 24h, 7d, 30d), cálculo de MTBF/MTTR/P95 e exportação em CSV/JSON.
- **Operação e observabilidade**: tema claro/escuro, grupos, modo compacto, modo TV (`?view=tv`), página pública somente leitura (`/status`), métricas Prometheus (`/metrics`) e hot reload via `SIGHUP`.

## Início Rápido

### Docker Compose (Recomendado)

```bash
git clone https://github.com/winstonajr/argus.git && cd argus
cp config.example.yaml config.yaml
cp .env.example .env
docker compose up -d
```

Acesse `http://localhost:8080`.

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

### Executando com Go

Pré-requisitos:
- Go 1.22+
- Permissão de socket raw para ping ICMP no Linux (opcional se usar apenas HTTP/TCP):
  ```bash
  sudo setcap cap_net_raw+ep ./bin/argus
  # ou para o grupo de usuários:
  sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"
  ```

Execução:
```bash
go run ./cmd/argus -config config.example.yaml

# ou compilando o binário:
make build
./bin/argus -config config.example.yaml
```

## Configuração

A precedência de configuração segue: **Variáveis de Ambiente > Arquivo YAML > Padrões Embutidos**.

### Variáveis de Ambiente

| Variável | Padrão | Descrição |
|---|---|---|
| `ARGUS_SERVER_ADDR` | `:8080` | Endereço e porta HTTP (ou `PORT=8080`) |
| `ARGUS_LANG` | `pt-BR` | Idioma da interface, diagnóstico e alertas (`pt-BR` ou `en`) |
| `ARGUS_TIMEZONE` | Local do sistema (`TZ`) | Fuso horário IANA para exibição dos horários (ex.: `America/Sao_Paulo`, `UTC`; YAML: `server.timezone`) |
| `ARGUS_SANTA_EASTER_EGG` | `false` | Ativa neve e trenó do Papai Noel com 9 renas quando todos os sistemas estão `DOWN` (YAML: `server.santa_easter_egg`) |
| `ARGUS_SERVER_AUTH_USERNAME` | `admin` | Usuário Basic Auth (ativo quando senha é definida) |
| `ARGUS_SERVER_AUTH_PASSWORD` | — | Senha Basic Auth (protege UI/API; `/status`, `/healthz` e `/metrics` permanecem públicos) |
| `ARGUS_SERVER_AUTH_TOKEN` | — | Token Bearer / `X-Argus-Token` para API |
| `ARGUS_CONFIG_PATH` | `./config.yaml` | Caminho do arquivo de configuração YAML |
| `ARGUS_STORAGE_PATH` | `./data/argus.db` | Caminho do banco SQLite |
| `ARGUS_STORAGE_RETENTION_CHECKS` | `168h` | Retenção de logs de checagens |
| `ARGUS_STORAGE_RETENTION_EVENTS` | `2160h` | Retenção do histórico de incidentes |
| `ARGUS_DEFAULTS_INTERVAL` | `30s` | Intervalo padrão entre checagens |
| `ARGUS_DEFAULTS_HTTP_TIMEOUT` | `5s` | Timeout para requisições HTTP |
| `ARGUS_DEFAULTS_PING_TIMEOUT` | `3s` | Timeout para ping ICMP / TCP |
| `ARGUS_DEFAULTS_FAIL_THRESHOLD` | `2` | Falhas consecutivas para marcar `DOWN` |
| `ARGUS_DEFAULTS_DEGRADED_LATENCY` | `2s` | Limite de latência para `DEGRADED` |
| `ARGUS_DEFAULTS_TLS_WARN_DAYS` | `15` | Dias antes do vencimento SSL/TLS para alertar `DEGRADED` |
| `ARGUS_ALERTS_COOLDOWN` | `5m` | Intervalo mínimo entre alertas repetidos |
| `ARGUS_ALERTS_NOTIFY_ON_DEGRADED` | `false` | Enviar alertas em transições `DEGRADED` |
| `ARGUS_ALERTS_WEBHOOK_URL` | — | URL de Webhook genérico (JSON POST) |
| `ARGUS_ALERTS_SLACK_WEBHOOK_URL` | — | Incoming Webhook URL do Slack |
| `ARGUS_ALERTS_DISCORD_WEBHOOK_URL` | — | Webhook URL do Discord |
| `ARGUS_ALERTS_TELEGRAM_BOT_TOKEN` | — | Token do Bot do Telegram |
| `ARGUS_ALERTS_TELEGRAM_CHAT_ID` | — | ID do chat/grupo do Telegram |
| `ARGUS_ALERTS_EMAIL_SMTP_HOST` | — | Host do servidor SMTP |
| `ARGUS_ALERTS_EMAIL_SMTP_PORT` | `587` | Porta SMTP (`587` STARTTLS, `465` TLS implícito) |
| `ARGUS_ALERTS_EMAIL_USERNAME` | — | Usuário SMTP |
| `ARGUS_ALERTS_EMAIL_PASSWORD` | — | Senha SMTP |
| `ARGUS_ALERTS_EMAIL_FROM` | — | Remetente dos alertas |
| `ARGUS_ALERTS_EMAIL_TO` | — | Destinatários separados por vírgula |

### Interpolação no `config.yaml`

O arquivo YAML suporta `${VAR}` e `${VAR:-fallback}`:

```yaml
server:
  addr: "${PORT:-:8080}"

storage:
  path: "${ARGUS_STORAGE_PATH:-./data/argus.db}"

systems:
  - id: "erp"
    name: "ERP Corporativo"
    group: "Produção"
    url: "${ERP_URL:-https://erp.empresa.local/health}"
    ip: "${ERP_INTERNAL_IP:-10.0.0.15}"
```

### Hot Reload

Recarregue o `config.yaml` sem derrubar o servidor ou perder o histórico em memória:

```bash
docker kill -s HUP argus
# ou localmente:
kill -HUP $(pidof argus)
```

## Endpoints

| Rota | Descrição |
|---|---|
| `GET /` | Painel de operações NOC |
| `GET /status` | Página de status pública (oculta IPs internos) |
| `GET /systems/{id}/history` | Histórico, linha do tempo e métricas (1h, 24h, 7d, 30d) |
| `GET /systems/{id}/export` | Exportação de histórico (`?period=30d&format=csv` ou `json`) |
| `GET /api/systems` | Lista de sistemas em JSON |
| `GET /api/systems/{id}` | Detalhes de um sistema em JSON |
| `GET /api/events` | Eventos recentes de mudança de estado |
| `POST /api/systems/{id}/maintenance` | Alterna modo de manutenção em runtime |
| `GET /metrics` | Métricas no formato Prometheus |
| `GET /healthz` | Healthcheck da aplicação e banco SQLite |

## Atalhos de Teclado

- `/` : focar no campo de busca.
- `Esc` : limpar busca e desfocar.
- `1` : filtrar todos os sistemas.
- `2` : filtrar sistemas fora do ar (`DOWN`).
- `3` : filtrar sistemas instáveis (`DEGRADED`).
- `4` : filtrar sistemas operacionais (`UP`).
- `t` : alternar tema escuro/claro.
- `v` : alternar modo TV.

## Desenvolvimento e Testes

```bash
go test -v -race ./...
go vet ./...
```

## Contribuição

O projeto segue **GitFlow** e [Conventional Commits](https://www.conventionalcommits.org/):

- Branches principais: `main` (releases) e `develop` (integração).
- Branches de trabalho: `feature/*`, `fix/*`, `docs/*`, `ci/*`, `chore/*`.
- Formato de commit: `<tipo>(escopo): descrição curta no imperativo` (ex.: `feat(notify): add slack webhook`, `fix(web): remove click interception on cards`).

## Licença

Distribuído sob a licença **MIT**. Consulte o arquivo [LICENSE](LICENSE) para mais detalhes.
