# 🛰️ Sentinela

Monitor simples, resiliente e moderno de disponibilidade de sistemas internos e serviços em nuvem.

Para cada sistema configurado, o **Sentinela** verifica de forma independente:
1. **DNS / Aplicação**: resolução de nome e requisição HTTP (suporte a rota `/health` dedicada ou modo base, com identificação de erros de borda como Cloudflare 502/520).
2. **IP / Máquina**: teste de conectividade direta na máquina via ping ICMP (ou porta TCP configurável).
3. **Diagnóstico Automático**: cruza os estados para determinar se a falha é no DNS, na aplicação (com a máquina ligada), ou se a VM/rede inteira caiu.
4. **Cards Independentes em HTMX**: cada card faz sua própria requisição em segundo plano a cada 15 segundos sem recarregar a página.
5. **Histórico e Linha do Tempo**: persistência de verificações e incidentes em SQLite local com visualização gráfica em blocos de tempo.
6. **Design NOC / Sala de Operações**: tema escuro por padrão, tipografia mono para métricas numéricas, paleta sóbria, atalhos de teclado e modo TV (`?view=tv`).

---

## 🚀 Como Executar

### 1. Pré-requisitos
- **Go 1.22+** instalado.
- (Opcional) Permissão de socket raw para ICMP ping no Linux:
  ```bash
  sudo setcap cap_net_raw+ep ./bin/sentinela
  # Ou permitir para o grupo de usuários no sistema:
  sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"
  ```

### 2. Rodando Localmente
```bash
# Modo desenvolvimento com Live Reload (Air):
make dev

# Compilar e executar normalmente:
make run

# Ou compilar o binário em bin/sentinela:
make build
./bin/sentinela -config config.example.yaml
```

Acesse no navegador:
👉 **http://localhost:8080**

---

## 🐳 Executando com Docker

O Sentinela possui Dockerfile multi-stage com a capability `cap_net_raw` pré-configurada:

```bash
# Construir a imagem
docker build -t sentinela:latest .

# Executar mapeando volume de dados e concedendo permissão de rede
docker run -d \
  --name sentinela \
  -p 8080:8080 \
  --cap-add=NET_RAW \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/config.example.yaml:/app/config.yaml:ro \
  sentinela:latest
```

---

## ⚙️ Configuração (`config.yaml`)

```yaml
server:
  addr: ":8080"

storage:
  path: "./data/sentinela.db"
  retention_checks: 168h   # 7 dias de logs detalhados
  retention_events: 2160h  # 90 dias de histórico de incidentes

defaults:
  interval: 30s            # Intervalo entre verificações pelo backend
  http_timeout: 5s         # Timeout para requisição HTTP
  ping_timeout: 3s         # Timeout para o ping ICMP
  fail_threshold: 2        # Falhas consecutivas para marcar como DOWN (evita alarmes falsos)
  degraded_latency: 2s     # Latência que sinaliza lentidão (DEGRADED)

systems:
  # Sistema com endpoint /health explícito (exige retorno 2xx)
  - id: "erp"
    name: "ERP Corporativo"
    url: "https://erp.empresa.local/health"
    health_path: "/health"
    ip: "10.0.0.15"

  # Sistema web comum (qualquer status < 500 indica servidor vivo)
  - id: "portal"
    name: "Portal do Cliente"
    url: "https://portal.empresa.local"
    ip: "10.0.0.22"

  # Host com firewall que bloqueia ICMP: usa verificação TCP na porta 22
  - id: "legado"
    name: "Servidor de Arquivos"
    ip: "10.0.1.5"
    tcp_port: 22
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

O projeto segue padrões rígidos de qualidade (verificação estática, detecção de race conditions e cobertura de testes):

```bash
# Rodar todos os testes unitários com race detector:
make test

# Rodar verificação estática do Go:
make vet
```
