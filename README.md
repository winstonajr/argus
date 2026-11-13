# 🛰️ Argus

Monitor simples, resiliente e moderno de disponibilidade de sistemas internos e serviços em nuvem.

Para cada sistema configurado, o **Argus** verifica de forma independente:
1. **DNS / Aplicação**: resolução de nome e requisição HTTP (suporte a rota `/health` dedicada ou modo base, com identificação de erros de borda como Cloudflare 502/520).
2. **IP / Máquina**: teste de conectividade direta na máquina via ping ICMP (ou porta TCP configurável).
3. **Diagnóstico Automático**: cruza os estados para determinar se a falha é no DNS, na aplicação (com a máquina ligada), ou se a VM/rede inteira caiu.
4. **Cards Independentes em HTMX**: cada card faz sua própria requisição em segundo plano a cada 15 segundos sem recarregar a página.
5. **Histórico e Linha do Tempo**: persistência de verificações e incidentes em SQLite local com visualização gráfica em blocos de tempo.
6. **Design NOC / Sala de Operações**: tema escuro por padrão, tipografia mono para métricas numéricas, paleta sóbria, atalhos de teclado e modo TV (`?view=tv`).

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
