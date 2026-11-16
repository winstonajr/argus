# 🛰️ Argus

Monitor simples, resiliente e moderno de disponibilidade de sistemas internos e serviços em nuvem.

Para cada sistema configurado, o **Argus** verifica de forma independente:
1. **DNS / Aplicação**: resolução de nome (com validação opcional de IP esperado via `expected_dns`) e requisição HTTP (`GET`, `HEAD`, `POST`, headers customizados, `expected_status`, `expected_body`, `json_path`), com identificação de erros de borda como Cloudflare 502/520–526.
2. **Certificado SSL/TLS**: monitoramento automático de validade e alerta antecipado de expiração (`tls_warn_days`).
3. **IP / Máquina**: teste de conectividade direta na máquina via ping ICMP, porta TCP ou endpoint interno HTTP(S).
4. **Diagnóstico Automático**: cruza os estados para determinar se a falha é no DNS, certificado TLS, borda Cloudflare, aplicação (com a máquina ligada) ou queda total da VM/rede.
5. **Tempo Real via SSE + Heartbeat & Sparkline**: atualizações instantâneas via Server-Sent Events (`/events`), reordenação automática de sistemas críticos para o topo, barra de 30 heartbeats e sparkline de latência nos cards.
6. **Notificações Multi-Canal**: alertas de queda e recuperação (com duração da queda) via Webhook, Slack, Discord, Telegram e E-mail (SMTP), com *cooldown* anti-flapping e supressão em modo de manutenção.
7. **Histórico, SLA e Exportação**: persistência em SQLite (WAL) com hidratação no restart, linha do tempo (1h, 24h, 7d, 30d), cálculo de MTBF/MTTR/P95 e exportação em CSV/JSON.
8. **Design NOC / Sala de Operações**: tema escuro por padrão, grupos/tags, modo compacto/tabela, alerta sonoro opcional, atalhos de teclado, modo TV (`?view=tv`) e página pública (`/status`).

---

## ⚡ Início Rápido (Local com Docker Compose)

Para rodar o Argus em qualquer máquina sem precisar instalar Go:

```bash
# 1. Clone o repositório
git clone https://github.com/winstonajr/argus.git && cd argus

# 2. Crie seus arquivos locais a partir dos exemplos
cp config.example.yaml config.yaml
cp .env.example .env

# 3. Suba o container com Docker Compose
docker compose up -d
```

Acesse no navegador:
👉 **http://localhost:8080**

---

## 🚀 Como Executar com Go

### 1. Pré-requisitos
- **Go 1.22+** instalado.
- (Opcional) Permissão de socket raw para ICMP ping no Linux:
  ```bash
  sudo setcap cap_net_raw+ep ./bin/argus
  # Ou permitir para o grupo de usuários no sistema:
  sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"
  ```

### 2. Rodando Localmente
```bash
# Executar diretamente com Go:
go run ./cmd/argus -config config.example.yaml

# Ou em modo desenvolvimento com Live Reload (Air):
air

# Ou compilar o binário estático:
go build -o bin/argus ./cmd/argus
./bin/argus -config config.example.yaml
```

*(Opcional: o `Makefile` inclui `make run`, `make dev`, `make test` e `make build`).*

---

## 🐳 Executando com Docker

Você pode rodar diretamente com `docker run` mapeando seu volume de dados e o arquivo de configuração:

```bash
# Executar mapeando volume de dados e concedendo permissão de rede
docker run -d \
  --name argus \
  -p 8080:8080 \
  --cap-add=NET_RAW \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  winajr/argus:latest
```

---

## ⚙️ Variáveis de Ambiente e Configuração

O Argus suporta configuração hierárquica: **Variáveis de Ambiente > Arquivo YAML > Padrões Embutidos**.

### Variáveis Suportadas (`.env` ou Environment)
Copie o arquivo `.env.example` para `.env` e altere como preferir:

| Variável | Padrão | Descrição |
|---|---|---|
| `ARGUS_SERVER_ADDR` | `:8080` | Endereço e porta HTTP (ou use `PORT=8080`) |
| `ARGUS_CONFIG_PATH` | `./config.yaml` | Caminho do arquivo de configuração YAML |
| `ARGUS_STORAGE_PATH` | `./data/argus.db` | Caminho do banco SQLite de métricas |
| `ARGUS_STORAGE_RETENTION_CHECKS` | `168h` (7 dias) | Retenção dos logs detalhados de checagens |
| `ARGUS_STORAGE_RETENTION_EVENTS` | `2160h` (90 dias) | Retenção do histórico de incidentes |
| `ARGUS_DEFAULTS_INTERVAL` | `30s` | Intervalo padrão entre checagens |
| `ARGUS_DEFAULTS_HTTP_TIMEOUT` | `5s` | Timeout para requisições HTTP |
| `ARGUS_DEFAULTS_PING_TIMEOUT` | `3s` | Timeout para ping ICMP / TCP |
| `ARGUS_DEFAULTS_FAIL_THRESHOLD` | `2` | Falhas consecutivas para marcar DOWN |
| `ARGUS_DEFAULTS_DEGRADED_LATENCY` | `2s` | Limite de latência para DEGRADED |
| `ARGUS_DEFAULTS_TLS_WARN_DAYS` | `15` | Dias antes do vencimento SSL/TLS para alertar DEGRADED |
| `ARGUS_ALERTS_COOLDOWN` | `5m` | Intervalo mínimo entre alertas repetidos (anti-flapping) |
| `ARGUS_ALERTS_NOTIFY_ON_DEGRADED` | `false` | Enviar alertas também para transições `DEGRADED` |
| `ARGUS_ALERTS_WEBHOOK_URL` | — | URL de Webhook genérico (JSON POST) |
| `ARGUS_ALERTS_SLACK_WEBHOOK_URL` | — | Incoming Webhook URL do Slack |
| `ARGUS_ALERTS_DISCORD_WEBHOOK_URL` | — | Webhook URL do Discord |
| `ARGUS_ALERTS_TELEGRAM_BOT_TOKEN` | — | Token do Bot do Telegram |
| `ARGUS_ALERTS_TELEGRAM_CHAT_ID` | — | ID do chat/grupo do Telegram |
| `ARGUS_ALERTS_EMAIL_SMTP_HOST` | — | Host do servidor SMTP para alertas por e-mail |
| `ARGUS_ALERTS_EMAIL_SMTP_PORT` | `587` | Porta SMTP |
| `ARGUS_ALERTS_EMAIL_USERNAME` | — | Usuário SMTP |
| `ARGUS_ALERTS_EMAIL_PASSWORD` | — | Senha SMTP |
| `ARGUS_ALERTS_EMAIL_FROM` | — | Remetente do e-mail de alerta |
| `ARGUS_ALERTS_EMAIL_TO` | — | Destinatários separados por vírgula (`a@x.com,b@x.com`) |

### Interpolação de Variáveis no `config.yaml`
Você pode usar a sintaxe `${VAR}` ou `${VAR:-fallback}` diretamente dentro do seu arquivo YAML:

```yaml
server:
  addr: "${PORT:-:8080}"

storage:
  path: "${ARGUS_STORAGE_PATH:-./data/argus.db}"

systems:
  - id: "erp"
    name: "ERP Corporativo"
    url: "${ERP_URL:-https://erp.empresa.local/health}"
    ip: "${ERP_INTERNAL_IP:-10.0.0.15}"
```

---

## 📡 Endpoints, API e Operação

- **Painel NOC**: `/`
- **Página de Status Pública (somente leitura, sem IPs internos)**: `/status`
- **Histórico e SLA do Sistema (1h, 24h, 7d, 30d)**: `/systems/{id}/history`
- **Exportação de Histórico**: `/systems/{id}/export?period=30d&format=csv` (ou `format=json`)
- **API JSON**:
  - `GET /api/systems` e `GET /api/systems/{id}`
  - `GET /api/events`
  - `POST /api/systems/{id}/maintenance` (alterna modo de manutenção em runtime)
- **Métricas Prometheus**: `GET /metrics`
- **Healthcheck**: `GET /healthz`
- **Hot Reload de Configuração (`SIGHUP`)**: recarregue o `config.yaml` sem derrubar o servidor ou perder o histórico em memória:
  ```bash
  docker kill -s HUP argus
  # ou localmente: kill -HUP $(pidof argus)
  ```

---

## ⌨️ Atalhos de Teclado na Interface

- `/` : Focar no campo de busca de sistemas.
- `Esc` : Limpar busca / desfocar.
- `1` : Filtrar **Todos** os sistemas.
- `2` : Filtrar apenas sistemas **Fora do ar** (DOWN).
- `3` : Filtrar sistemas **Instáveis** (DEGRADED).
- `4` : Filtrar sistemas **Operacionais** (UP).
- `t` : Alternar entre tema Escuro e Claro.
- `v` : Alternar **Modo TV** (aumenta escala para telões de operação).

---

## 🧪 Testes e Qualidade

```bash
# Executar todos os testes com detector de concorrência:
go test -v -race ./...

# Executar verificação estática do Go:
go vet ./...
```
