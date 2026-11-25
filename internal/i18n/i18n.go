package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

const (
	LangPT = "pt-BR"
	LangEN = "en"
)

type Messages struct {
	Lang                    string
	DashboardTitle          string
	SearchPlaceholder       string
	FilterAll               string
	FilterDown              string
	FilterDegraded          string
	FilterUp                string
	AllGroups               string
	FilterByGroup           string
	Live                    string
	SortPriorityTitle       string
	SortActiveTitle         string
	ViewCompactTitle        string
	SoundMutedTitle         string
	SoundActiveTitle        string
	TestAlertTitle          string
	TVModeTitle             string
	ThemeToggleTitle        string
	ThemeAuto               string
	ThemeGroupBase          string
	ThemeGroupStyles        string
	ThemeDark               string
	ThemeLight              string
	ThemeLatte              string
	ShortcutsTitle          string
	LangToggleTitle         string
	StatUp                  string
	StatDegraded            string
	StatDown                string
	StatTotal               string
	LastCycle               string
	ArgusUptime             string
	StatusUpUpper           string
	StatusDegradedUpper     string
	StatusDownUpper         string
	StatusUnknownUpper      string
	MaintenanceUpper        string
	Waiting                 string
	WaitingCap              string
	FailedUpper             string
	NoPermissionUpper       string
	RefusedUpper            string
	TimeoutUpper            string
	RecentHistoryAria       string
	HeartbeatSummaryFmt     string
	LatencyTrendAria        string
	LastCheck               string
	TLSCertValidity         string
	HistoryLink             string
	NoResponse              string
	EmptyFilterTitle        string
	ClearFilters            string
	HistorySuffix           string
	BackToDashboard         string
	Period1h                string
	Period24h               string
	Period7d                string
	Period30d               string
	Period1hLabel           string
	Period24hLabel          string
	Period7dLabel           string
	Period30dLabel          string
	CheckNow                string
	Checking                string
	Scheduled               string
	EnableMaintenance       string
	DisableMaintenance      string
	Maint1h                 string
	Maint4h                 string
	MaintIndefinite         string
	AllTargetsOperational   string
	SystemDown              string
	InstabilityDetected     string
	WaitingInitialTelemetry string
	ForDurationPrefix       string
	MetricDNSUptime         string
	MetricIPUptime          string
	MetricIncidents         string
	MetricLatencyAvgP95     string
	MetricMTTRMTBF          string
	MetricTarget            string
	TimelineTitle           string
	LatencyChartTitle       string
	DNSAppLabel             string
	IPMachineLabel          string
	OperationalLower        string
	EventsSectionTitle      string
	DurationLabel           string
	NoIncidentsInPeriod     string
	FailedChecksTitle       string
	ColTime                 string
	ColType                 string
	ColCategory             string
	ColCodeLatency          string
	ColErrorMessage         string
	NoFailedChecksInPeriod  string
	LoadMore                string
	PublicStatusTitle       string
	UpdatedAt               string
	AllSystemsOperational   string
	SystemsDownCountFmt     string
	PartialDegradation      string
	StatusOperational       string
	StatusDegraded          string
	StatusDown              string
	StatusNoData            string
	RecentIncidentsTitle    string
	Uptime90dLabel          string
	TabSystemsDownFmt       string
	TabOperational          string
	ToastTestSentFmt        string
	ToastTestFailed         string
	ToastMaintEnabled       string
	ToastMaintDisabled      string
	ToastCheckDone          string
	ToastRateLimit          string
	ShortcutsModalTitle     string
	ShortcutSearch          string
	ShortcutFilter          string
	ShortcutTheme           string
	ShortcutTV              string
	ShortcutHelp            string
}

var ptBR = Messages{
	Lang:                    LangPT,
	DashboardTitle:          "Painel de Operações",
	SearchPlaceholder:       "Buscar sistemas... (pressione /)",
	FilterAll:               "Todos",
	FilterDown:              "Fora do ar",
	FilterDegraded:          "Instável",
	FilterUp:                "Operacional",
	AllGroups:               "Todos os grupos",
	FilterByGroup:           "Filtrar por grupo",
	Live:                    "Ao vivo",
	SortPriorityTitle:       "Ordenar por prioridade (críticos no topo)",
	SortActiveTitle:         "Ordem por prioridade ativa (críticos no topo) — clique para ordem manual/alfabética",
	ViewCompactTitle:        "Alternar visualização Cards / Compacto",
	SoundMutedTitle:         "Alerta sonoro (Silenciado)",
	SoundActiveTitle:        "Alerta sonoro (Ativo)",
	TestAlertTitle:          "Testar envio de alertas",
	TVModeTitle:             "Modo TV / Sala de Operações",
	ThemeToggleTitle:        "Escolher tema",
	ThemeAuto:               "Automático",
	ThemeGroupBase:          "Padrão",
	ThemeGroupStyles:        "Estilos",
	ThemeDark:               "Escuro",
	ThemeLight:              "Claro",
	ThemeLatte:              "Latte (Quente)",
	ShortcutsTitle:          "Atalhos de teclado (?)",
	LangToggleTitle:         "Alternar idioma (PT / EN)",
	StatUp:                  "Operacionais",
	StatDegraded:            "Instáveis",
	StatDown:                "Fora do ar",
	StatTotal:               "Total Monitorado",
	LastCycle:               "Último ciclo",
	ArgusUptime:             "Uptime Argus",
	StatusUpUpper:           "OPERACIONAL",
	StatusDegradedUpper:     "INSTÁVEL",
	StatusDownUpper:         "FORA DO AR",
	StatusUnknownUpper:      "SEM DADOS",
	MaintenanceUpper:        "MANUTENÇÃO",
	Waiting:                 "aguardando...",
	WaitingCap:              "Aguardando...",
	FailedUpper:             "FALHA",
	NoPermissionUpper:       "SEM PERMISSÃO",
	RefusedUpper:            "RECUSADO",
	TimeoutUpper:            "TIMEOUT",
	RecentHistoryAria:       "Histórico recente",
	HeartbeatSummaryFmt:     "%s: %d de %d verificações operacionais",
	LatencyTrendAria:        "Tendência de latência",
	LastCheck:               "Última consulta",
	TLSCertValidity:         "Validade do certificado TLS",
	HistoryLink:             "Histórico →",
	NoResponse:              "Sem resposta",
	EmptyFilterTitle:        "Nenhum sistema corresponde aos filtros selecionados.",
	ClearFilters:            "Limpar filtros",
	HistorySuffix:           "Histórico",
	BackToDashboard:         "Voltar ao painel principal",
	Period1h:                "Última 1h",
	Period24h:               "Últimas 24h",
	Period7d:                "Últimos 7 dias",
	Period30d:               "Últimos 30 dias",
	Period1hLabel:           "Última 1 hora",
	Period24hLabel:          "Últimas 24 horas",
	Period7dLabel:           "Últimos 7 dias",
	Period30dLabel:          "Últimos 30 dias",
	CheckNow:                "Testar Agora",
	Checking:                "Verificando...",
	Scheduled:               "Agendado!",
	EnableMaintenance:       "Modo Manutenção",
	DisableMaintenance:      "Desativar Manutenção",
	Maint1h:                 "Manutenção 1h",
	Maint4h:                 "Manutenção 4h",
	MaintIndefinite:         "Até desativar",
	AllTargetsOperational:   "Todos os alvos operacionais",
	SystemDown:              "Sistema fora do ar",
	InstabilityDetected:     "Instabilidade detectada",
	WaitingInitialTelemetry: "Aguardando telemetria inicial...",
	ForDurationPrefix:       "há",
	MetricDNSUptime:         "Uptime DNS/HTTP",
	MetricIPUptime:          "Uptime IP/Host",
	MetricIncidents:         "Incidentes Registrados",
	MetricLatencyAvgP95:     "Latência Média / P95",
	MetricMTTRMTBF:          "MTTR / MTBF",
	MetricTarget:            "Alvo Configurado",
	TimelineTitle:           "Linha do Tempo",
	LatencyChartTitle:       "Tendência de Latência",
	DNSAppLabel:             "DNS / Aplicação",
	IPMachineLabel:          "IP / Máquina",
	OperationalLower:        "operacional",
	EventsSectionTitle:      "Histórico de Incidentes e Mudanças de Estado",
	DurationLabel:           "Duração",
	NoIncidentsInPeriod:     "Nenhum incidente registrado no período selecionado. O sistema operou com 100% de estabilidade.",
	FailedChecksTitle:       "Verificações com Falha",
	ColTime:                 "Horário",
	ColType:                 "Tipo",
	ColCategory:             "Categoria",
	ColCodeLatency:          "Código / Latência",
	ColErrorMessage:         "Mensagem de Erro",
	NoFailedChecksInPeriod:  "Nenhuma verificação com falha gravada no período.",
	LoadMore:                "Carregar mais",
	PublicStatusTitle:       "Status dos Serviços",
	UpdatedAt:               "Atualizado em",
	AllSystemsOperational:   "Todos os sistemas operacionais",
	SystemsDownCountFmt:     "%d sistema(s) com indisponibilidade",
	PartialDegradation:      "Degradação parcial detectada",
	StatusOperational:       "Operacional",
	StatusDegraded:          "Instável",
	StatusDown:              "Indisponível",
	StatusNoData:            "Sem dados",
	RecentIncidentsTitle:    "Incidentes Recentes",
	Uptime90dLabel:          "Disponibilidade recente",
	TabSystemsDownFmt:       "(%d) Argus — Sistemas Fora",
	TabOperational:          "Argus — Operacional",
	ToastTestSentFmt:        "Teste enviado (%d canal(is))",
	ToastTestFailed:         "Falha ao enviar teste",
	ToastMaintEnabled:       "Modo manutenção ativado",
	ToastMaintDisabled:      "Modo manutenção desativado",
	ToastCheckDone:          "Verificação concluída",
	ToastRateLimit:          "Aguarde alguns segundos antes de tentar novamente",
	ShortcutsModalTitle:     "Atalhos de Teclado",
	ShortcutSearch:          "Focar na busca / limpar filtro",
	ShortcutFilter:          "Filtrar por status (Todos, Fora, Instável, Operacional)",
	ShortcutTheme:           "Alternar tema claro / escuro",
	ShortcutTV:              "Alternar modo TV / NOC",
	ShortcutHelp:            "Exibir / ocultar ajuda de atalhos",
}

var enUS = Messages{
	Lang:                    LangEN,
	DashboardTitle:          "Operations Console",
	SearchPlaceholder:       "Search systems... (press /)",
	FilterAll:               "All",
	FilterDown:              "Down",
	FilterDegraded:          "Degraded",
	FilterUp:                "Operational",
	AllGroups:               "All groups",
	FilterByGroup:           "Filter by group",
	Live:                    "Live",
	SortPriorityTitle:       "Sort by priority (critical on top)",
	SortActiveTitle:         "Priority sort active (critical on top) — click for manual/alphabetical order",
	ViewCompactTitle:        "Toggle Cards / Compact view",
	SoundMutedTitle:         "Sound alert (Muted)",
	SoundActiveTitle:        "Sound alert (Active)",
	TestAlertTitle:          "Send test alert",
	TVModeTitle:             "TV / NOC Wall Mode",
	ThemeToggleTitle:        "Choose theme",
	ThemeAuto:               "Automatic",
	ThemeGroupBase:          "Default",
	ThemeGroupStyles:        "Styles",
	ThemeDark:               "Dark",
	ThemeLight:              "Light",
	ThemeLatte:              "Latte (Warm)",
	ShortcutsTitle:          "Keyboard shortcuts (?)",
	LangToggleTitle:         "Switch language (PT / EN)",
	StatUp:                  "Operational",
	StatDegraded:            "Degraded",
	StatDown:                "Down",
	StatTotal:               "Total Monitored",
	LastCycle:               "Last cycle",
	ArgusUptime:             "Argus Uptime",
	StatusUpUpper:           "OPERATIONAL",
	StatusDegradedUpper:     "DEGRADED",
	StatusDownUpper:         "DOWN",
	StatusUnknownUpper:      "NO DATA",
	MaintenanceUpper:        "MAINTENANCE",
	Waiting:                 "waiting...",
	WaitingCap:              "Waiting...",
	FailedUpper:             "FAILED",
	NoPermissionUpper:       "NO PERMISSION",
	RefusedUpper:            "REFUSED",
	TimeoutUpper:            "TIMEOUT",
	RecentHistoryAria:       "Recent history",
	HeartbeatSummaryFmt:     "%s: %d of %d checks operational",
	LatencyTrendAria:        "Latency trend",
	LastCheck:               "Last check",
	TLSCertValidity:         "TLS certificate validity",
	HistoryLink:             "History →",
	NoResponse:              "No response",
	EmptyFilterTitle:        "No systems match the selected filters.",
	ClearFilters:            "Clear filters",
	HistorySuffix:           "History",
	BackToDashboard:         "Back to dashboard",
	Period1h:                "Last 1h",
	Period24h:               "Last 24h",
	Period7d:                "Last 7 days",
	Period30d:               "Last 30 days",
	Period1hLabel:           "Last 1 hour",
	Period24hLabel:          "Last 24 hours",
	Period7dLabel:           "Last 7 days",
	Period30dLabel:          "Last 30 days",
	CheckNow:                "Check Now",
	Checking:                "Checking...",
	Scheduled:               "Scheduled!",
	EnableMaintenance:       "Maintenance Mode",
	DisableMaintenance:      "Disable Maintenance",
	Maint1h:                 "Maintenance 1h",
	Maint4h:                 "Maintenance 4h",
	MaintIndefinite:         "Until disabled",
	AllTargetsOperational:   "All targets operational",
	SystemDown:              "System down",
	InstabilityDetected:     "Instability detected",
	WaitingInitialTelemetry: "Waiting for initial telemetry...",
	ForDurationPrefix:       "for",
	MetricDNSUptime:         "DNS/HTTP Uptime",
	MetricIPUptime:          "IP/Host Uptime",
	MetricIncidents:         "Recorded Incidents",
	MetricLatencyAvgP95:     "Avg / P95 Latency",
	MetricMTTRMTBF:          "MTTR / MTBF",
	MetricTarget:            "Configured Target",
	TimelineTitle:           "Timeline",
	LatencyChartTitle:       "Latency Trend",
	DNSAppLabel:             "DNS / Application",
	IPMachineLabel:          "IP / Host",
	OperationalLower:        "operational",
	EventsSectionTitle:      "Incident & State Change History",
	DurationLabel:           "Duration",
	NoIncidentsInPeriod:     "No incidents recorded in the selected period. The system operated with 100% stability.",
	FailedChecksTitle:       "Failed Checks",
	ColTime:                 "Time",
	ColType:                 "Type",
	ColCategory:             "Category",
	ColCodeLatency:          "Code / Latency",
	ColErrorMessage:         "Error Message",
	NoFailedChecksInPeriod:  "No failed checks recorded in this period.",
	LoadMore:                "Load more",
	PublicStatusTitle:       "Service Status",
	UpdatedAt:               "Updated at",
	AllSystemsOperational:   "All systems operational",
	SystemsDownCountFmt:     "%d system(s) experiencing outage",
	PartialDegradation:      "Partial degradation detected",
	StatusOperational:       "Operational",
	StatusDegraded:          "Degraded",
	StatusDown:              "Outage",
	StatusNoData:            "No data",
	RecentIncidentsTitle:    "Recent Incidents",
	Uptime90dLabel:          "Recent availability",
	TabSystemsDownFmt:       "(%d) Argus — Systems Down",
	TabOperational:          "Argus — Operational",
	ToastTestSentFmt:        "Test alert sent (%d channel(s))",
	ToastTestFailed:         "Failed to send test alert",
	ToastMaintEnabled:       "Maintenance mode enabled",
	ToastMaintDisabled:      "Maintenance mode disabled",
	ToastCheckDone:          "Check completed",
	ToastRateLimit:          "Please wait a few seconds before retrying",
	ShortcutsModalTitle:     "Keyboard Shortcuts",
	ShortcutSearch:          "Focus search / clear filter",
	ShortcutFilter:          "Filter by status (All, Down, Degraded, Operational)",
	ShortcutTheme:           "Toggle light / dark theme",
	ShortcutTV:              "Toggle TV / NOC mode",
	ShortcutHelp:            "Show / hide keyboard shortcuts",
}

var defaultLang atomic.Value

func init() {
	defaultLang.Store(Normalize(os.Getenv("ARGUS_LANG")))
}

func Normalize(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(l, "en") {
		return LangEN
	}
	return LangPT
}

func SetDefault(lang string) {
	defaultLang.Store(Normalize(lang))
}

func CurrentLang() string {
	if v, ok := defaultLang.Load().(string); ok && v != "" {
		return v
	}
	return LangPT
}

func Current() Messages {
	return For(CurrentLang())
}

func For(lang string) Messages {
	if Normalize(lang) == LangEN {
		return enUS
	}
	return ptBR
}

func IsEN() bool {
	return CurrentLang() == LangEN
}

func Pick(pt, en string) string {
	if IsEN() {
		return en
	}
	return pt
}

func Pickf(ptFmt, enFmt string, args ...any) string {
	if IsEN() {
		return fmt.Sprintf(enFmt, args...)
	}
	return fmt.Sprintf(ptFmt, args...)
}
