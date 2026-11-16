package web

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/monitor"
	"argus/internal/storage"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

type sseEvent struct {
	Event string
	Data  string
}

// Server handles HTTP presentation and HTMX endpoints.
type Server struct {
	cfgMu       sync.RWMutex
	cfg         *config.Config
	store       *monitor.Store
	storage     *storage.DB
	mux         *http.ServeMux
	indexTmpl   *template.Template
	cardTmpl    *template.Template
	statsTmpl   *template.Template
	historyTmpl *template.Template
	statusTmpl  *template.Template
	iconsSVG    template.HTML
	sseMu       sync.RWMutex
	sseClients  map[chan sseEvent]struct{}
}

// NewServer initializes the HTTP routing and parses embedded templates.
func NewServer(cfg *config.Config, store *monitor.Store, db *storage.DB) (*Server, error) {
	s := &Server{
		cfg:        cfg,
		store:      store,
		storage:    db,
		mux:        http.NewServeMux(),
		sseClients: make(map[chan sseEvent]struct{}),
	}

	// Read inline SVG icons
	iconsData, err := staticFS.ReadFile("static/icons.svg")
	if err != nil {
		return nil, fmt.Errorf("read icons.svg: %w", err)
	}
	s.iconsSVG = template.HTML(iconsData) //nolint:gosec

	// Parse templates
	if err := s.parseTemplates(); err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s.routes()

	if store != nil {
		store.SubscribeChecks(func(sysID string, kind string, res checker.Result) {
			s.broadcastSystemUpdate(sysID)
		})
	}

	return s, nil
}

func (s *Server) parseTemplates() error {
	var err error

	s.indexTmpl, err = template.ParseFS(templateFS, "templates/layout.html", "templates/index.html")
	if err != nil {
		return fmt.Errorf("parse index tmpl: %w", err)
	}

	s.cardTmpl, err = template.ParseFS(templateFS, "templates/card.html")
	if err != nil {
		return fmt.Errorf("parse card tmpl: %w", err)
	}

	s.statsTmpl, err = template.ParseFS(templateFS, "templates/stats.html")
	if err != nil {
		return fmt.Errorf("parse stats tmpl: %w", err)
	}

	s.historyTmpl, err = template.ParseFS(templateFS, "templates/layout.html", "templates/history.html")
	if err != nil {
		return fmt.Errorf("parse history tmpl: %w", err)
	}

	s.statusTmpl, err = template.ParseFS(templateFS, "templates/layout.html", "templates/status.html")
	if err != nil {
		return fmt.Errorf("parse status tmpl: %w", err)
	}

	return nil
}

func (s *Server) routes() {
	// Static assets
	subStatic, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("failed to create static sub FS: %v", err)
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(subStatic))))

	// Web endpoints
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /cards/{id}", s.handleCard)
	s.mux.HandleFunc("GET /stats", s.handleStats)
	s.mux.HandleFunc("GET /events", s.handleSSE)
	s.mux.HandleFunc("GET /status", s.handlePublicStatus)
	s.mux.HandleFunc("GET /systems/{id}/history", s.handleHistory)
	s.mux.HandleFunc("GET /systems/{id}/export", s.handleExport)

	// API and Health
	s.mux.HandleFunc("GET /api/systems", s.handleAPISystems)
	s.mux.HandleFunc("GET /api/systems/{id}", s.handleAPISystem)
	s.mux.HandleFunc("POST /api/systems/{id}/maintenance", s.handleToggleMaintenance)
	s.mux.HandleFunc("GET /api/events", s.handleAPIEvents)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
}

// UpdateConfig updates the server's configuration reference safely at runtime.
func (s *Server) UpdateConfig(cfg *config.Config) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Handler: GET /
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()

	groupSet := make(map[string]struct{})
	for _, sys := range systems {
		if g := strings.TrimSpace(sys.Group); g != "" {
			groupSet[g] = struct{}{}
		}
	}
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	sort.SliceStable(systems, func(i, j int) bool {
		ni := strings.ToLower(systems[i].Name)
		nj := strings.ToLower(systems[j].Name)
		if ni != nj {
			return ni < nj
		}
		return systems[i].ID < systems[j].ID
	})

	statsHTML, err := s.renderStatsHTML()
	if err != nil {
		log.Printf("[ERROR] render index stats: %v", err)
	}

	var cardsBuf bytes.Buffer
	for _, sys := range systems {
		vm := toCardViewModel(sys)
		vm.JustChanged = false
		if err := s.cardTmpl.Execute(&cardsBuf, vm); err != nil {
			log.Printf("[ERROR] render index card %s: %v", sys.ID, err)
		}
	}

	data := struct {
		Title      string
		IconsSVG   template.HTML
		HideSearch bool
		Groups     []string
		StatsHTML  template.HTML
		CardsHTML  template.HTML
	}{
		Title:      "Painel de Operações",
		IconsSVG:   s.iconsSVG,
		HideSearch: false,
		Groups:     groups,
		StatsHTML:  template.HTML(statsHTML),       //nolint:gosec
		CardsHTML:  template.HTML(cardsBuf.String()), //nolint:gosec
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render index: %v", err)
	}
}

// Handler: GET /cards/{id}
func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, "Sistema não encontrado", http.StatusNotFound)
		return
	}

	vm := toCardViewModel(snap)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.cardTmpl.Execute(w, vm); err != nil {
		log.Printf("[ERROR] render card %s: %v", id, err)
	}
}

// Handler: GET /stats
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	htmlStr, err := s.renderStatsHTML()
	if err != nil {
		log.Printf("[ERROR] render stats: %v", err)
		http.Error(w, "Erro ao renderizar métricas", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(htmlStr))
}

func (s *Server) renderStatsHTML() (string, error) {
	stats := s.store.GetStats()

	lastCheckStr := "—"
	if !stats.LastCheck.IsZero() {
		lastCheckStr = stats.LastCheck.Format("15:04:05")
	}

	uptimeDur := time.Since(stats.UptimeStart).Truncate(time.Second)
	uptimeStr := formatDurationShort(uptimeDur)

	data := struct {
		UpCount                 int
		DegradedCount           int
		DownCount               int
		Total                   int
		WorstStatus             string
		LastCheckFormatted      string
		UptimeDurationFormatted string
	}{
		UpCount:                 stats.UpCount,
		DegradedCount:           stats.DegradedCount,
		DownCount:               stats.DownCount,
		Total:                   stats.Total,
		WorstStatus:             string(stats.WorstStatus),
		LastCheckFormatted:      lastCheckStr,
		UptimeDurationFormatted: uptimeStr,
	}

	var buf bytes.Buffer
	if err := s.statsTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Handler: GET /events (Server-Sent Events)
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan sseEvent, 64)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) broadcastSystemUpdate(sysID string) {
	s.sseMu.RLock()
	hasClients := len(s.sseClients) > 0
	s.sseMu.RUnlock()
	if !hasClients {
		return
	}

	snap, ok := s.store.GetSystem(sysID)
	if !ok {
		return
	}

	var cardBuf bytes.Buffer
	if err := s.cardTmpl.Execute(&cardBuf, toCardViewModel(snap)); err != nil {
		return
	}

	cardPayload, err := json.Marshal(struct {
		ID   string `json:"id"`
		HTML string `json:"html"`
	}{
		ID:   sysID,
		HTML: cardBuf.String(),
	})
	if err != nil {
		return
	}

	events := []sseEvent{{Event: "card", Data: string(cardPayload)}}

	if statsHTML, err := s.renderStatsHTML(); err == nil {
		if statsPayload, err := json.Marshal(struct {
			HTML string `json:"html"`
		}{HTML: statsHTML}); err == nil {
			events = append(events, sseEvent{Event: "stats", Data: string(statsPayload)})
		}
	}

	s.sseMu.RLock()
	for ch := range s.sseClients {
		for _, ev := range events {
			select {
			case ch <- ev:
			default:
			}
		}
	}
	s.sseMu.RUnlock()
}

// Handler: GET /systems/{id}/history
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, "Sistema não encontrado", http.StatusNotFound)
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}

	var duration time.Duration
	var periodLabel string
	var bucketCount int

	switch period {
	case "1h":
		duration = 1 * time.Hour
		periodLabel = "Última 1 hora"
		bucketCount = 30
	case "7d":
		duration = 7 * 24 * time.Hour
		periodLabel = "Últimos 7 dias"
		bucketCount = 84 // 12 buckets per day
	case "30d":
		duration = 30 * 24 * time.Hour
		periodLabel = "Últimos 30 dias"
		bucketCount = 90 // 3 buckets per day
	default:
		period = "24h"
		duration = 24 * time.Hour
		periodLabel = "Últimas 24 horas"
		bucketCount = 60
	}

	to := time.Now().UTC()
	from := to.Add(-duration)

	ctx := r.Context()
	var events []storage.StoredEvent
	var failed []storage.StoredCheck
	var buckets []storage.TimelineBucket
	stats := storage.SystemHistoryStats{DNSUptime: 100.0, IPUptime: 100.0}

	if s.storage != nil {
		var err error
		events, err = s.storage.ListEvents(ctx, id, from, to, 100)
		if err != nil {
			log.Printf("[WARN] list events: %v", err)
		}

		failed, err = s.storage.ListFailedChecks(ctx, id, from, to, 50, 0)
		if err != nil {
			log.Printf("[WARN] list failed checks: %v", err)
		}

		s.cfgMu.RLock()
		degLatMs := s.cfg.Defaults.DegradedLatency.Milliseconds()
		s.cfgMu.RUnlock()
		buckets, err = s.storage.GetTimelineBucketsWithLatency(ctx, id, from, to, bucketCount, degLatMs)
		if err != nil {
			log.Printf("[WARN] timeline buckets: %v", err)
		}

		stats, err = s.storage.GetSystemUptimeStats(ctx, id, from, to)
		if err != nil {
			log.Printf("[WARN] uptime stats: %v", err)
		}
	}

	// Prepare ViewModels
	eventVMs := make([]eventViewModel, len(events))
	for i, e := range events {
		eventVMs[i] = eventViewModel{
			Kind:                 strings.ToUpper(e.Kind),
			FromStatus:           e.FromStatus,
			ToStatus:             e.ToStatus,
			ToStatusLower:        strings.ToLower(e.ToStatus),
			Diagnosis:            e.Diagnosis,
			Detail:               e.Detail,
			OccurredAtFormatted:  e.OccurredAt.Local().Format("02/01 15:04:05"),
			DurationStr:          formatDurationShort(e.Duration),
		}
	}

	failedVMs := make([]failedCheckViewModel, len(failed))
	for i, f := range failed {
		failedVMs[i] = failedCheckViewModel{
			Kind:               strings.ToUpper(f.Kind),
			StatusCode:         f.StatusCode,
			LatencyMs:          f.LatencyMs,
			ErrorCat:           f.ErrorCat,
			ErrorMsg:           f.ErrorMsg,
			CheckedAtFormatted: f.CheckedAt.Local().Format("02/01 15:04:05"),
		}
	}

	bucketVMs := make([]timelineBucketViewModel, len(buckets))
	for i, b := range buckets {
		bucketVMs[i] = timelineBucketViewModel{
			DNSStatus:      b.DNSStatus,
			DNSStatusLower: strings.ToLower(b.DNSStatus),
			IPStatus:       b.IPStatus,
			IPStatusLower:  strings.ToLower(b.IPStatus),
			TimeFormatted:  b.StartTime.Local().Format("15:04"),
		}
	}

	mttrStr := "—"
	if stats.MTTR > 0 {
		mttrStr = formatDurationShort(stats.MTTR)
	}
	mtbfStr := "—"
	if stats.MTBF > 0 {
		mtbfStr = formatDurationShort(stats.MTBF)
	}

	data := struct {
		Title        string
		IconsSVG     template.HTML
		HideSearch   bool
		Groups       []string
		System       monitor.SystemSnapshot
		Period       string
		PeriodLabel  string
		Stats        storage.SystemHistoryStats
		MTTRStr      string
		MTBFStr      string
		Events       []eventViewModel
		FailedChecks []failedCheckViewModel
		Buckets      []timelineBucketViewModel
	}{
		Title:        fmt.Sprintf("%s — Histórico", snap.Name),
		IconsSVG:     s.iconsSVG,
		HideSearch:   true,
		System:       snap,
		Period:       period,
		PeriodLabel:  periodLabel,
		Stats:        stats,
		MTTRStr:      mttrStr,
		MTBFStr:      mtbfStr,
		Events:       eventVMs,
		FailedChecks: failedVMs,
		Buckets:      bucketVMs,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.historyTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render history: %v", err)
	}
}

// Handler: GET /api/systems/{id}
func (s *Server) handleAPISystem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, `{"error":"system not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

// Handler: GET /api/systems
func (s *Server) handleAPISystems(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(systems)
}

// Handler: POST /api/systems/{id}/maintenance
func (s *Server) handleToggleMaintenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, `{"error":"system not found"}`, http.StatusNotFound)
		return
	}

	next := !snap.InMaintenance
	if q := r.URL.Query().Get("enabled"); q != "" {
		next = q == "true" || q == "1"
	}
	s.store.SetMaintenance(id, next)
	s.broadcastSystemUpdate(id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"system_id":      id,
		"in_maintenance": next,
	})
}

// Handler: GET /api/events
func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	var events []storage.StoredEvent
	if s.storage != nil {
		var err error
		events, err = s.storage.ListAllRecentEvents(r.Context(), 100)
		if err != nil {
			http.Error(w, `{"error":"failed to load events"}`, http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

// Handler: GET /systems/{id}/export?period=24h&format=csv|json
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, "Sistema não encontrado", http.StatusNotFound)
		return
	}

	period := r.URL.Query().Get("period")
	var duration time.Duration
	switch period {
	case "1h":
		duration = 1 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	default:
		period = "24h"
		duration = 24 * time.Hour
	}

	to := time.Now().UTC()
	from := to.Add(-duration)
	ctx := r.Context()

	var events []storage.StoredEvent
	stats := storage.SystemHistoryStats{DNSUptime: 100.0, IPUptime: 100.0}
	if s.storage != nil {
		events, _ = s.storage.ListEvents(ctx, id, from, to, 1000)
		stats, _ = s.storage.GetSystemUptimeStats(ctx, id, from, to)
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"argus-%s-%s.csv\"", snap.ID, period))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"occurred_at", "kind", "from_status", "to_status", "diagnosis", "detail", "duration_seconds"})
		for _, ev := range events {
			_ = cw.Write([]string{
				ev.OccurredAt.Format(time.RFC3339),
				ev.Kind,
				ev.FromStatus,
				ev.ToStatus,
				ev.Diagnosis,
				ev.Detail,
				fmt.Sprintf("%.0f", ev.Duration.Seconds()),
			})
		}
		cw.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"argus-%s-%s.json\"", snap.ID, period))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"system_id": snap.ID,
		"name":      snap.Name,
		"period":    period,
		"stats":     stats,
		"events":    events,
	})
}

// Handler: GET /status (Public read-only status page)
func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()
	stats := s.store.GetStats()

	overallLower := "up"
	overallLabel := "Todos os sistemas operacionais"
	if stats.DownCount > 0 {
		overallLower = "down"
		overallLabel = fmt.Sprintf("%d sistema(s) com indisponibilidade", stats.DownCount)
	} else if stats.DegradedCount > 0 {
		overallLower = "degraded"
		overallLabel = "Degradação parcial detectada"
	}

	type publicSystemVM struct {
		Name        string
		Group       string
		StatusLower string
		StatusLabel string
		Uptime1h    float64
	}

	vms := make([]publicSystemVM, 0, len(systems))
	for _, sys := range systems {
		label := "Sem dados"
		switch sys.WorstStatus {
		case monitor.StatusUP:
			label = "Operacional"
		case monitor.StatusDegraded:
			label = "Instável"
		case monitor.StatusDown:
			label = "Indisponível"
		}
		vms = append(vms, publicSystemVM{
			Name:        sys.Name,
			Group:       sys.Group,
			StatusLower: strings.ToLower(string(sys.WorstStatus)),
			StatusLabel: label,
			Uptime1h:    sys.Uptime1h,
		})
	}

	lastCheckStr := "—"
	if !stats.LastCheck.IsZero() {
		lastCheckStr = stats.LastCheck.Local().Format("02/01/2006 15:04:05")
	}

	data := struct {
		Title              string
		IconsSVG           template.HTML
		HideSearch         bool
		Groups             []string
		OverallStatusLower string
		OverallLabel       string
		LastCheckFormatted string
		Systems            []publicSystemVM
	}{
		Title:              "Status dos Serviços",
		IconsSVG:           s.iconsSVG,
		HideSearch:         true,
		OverallStatusLower: overallLower,
		OverallLabel:       overallLabel,
		LastCheckFormatted: lastCheckStr,
		Systems:            vms,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.statusTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render status: %v", err)
	}
}

// Handler: GET /metrics (Prometheus exposition format)
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var b strings.Builder
	b.WriteString("# HELP argus_system_up System health state (1=UP, 0.5=DEGRADED, 0=DOWN)\n")
	b.WriteString("# TYPE argus_system_up gauge\n")
	for _, sys := range systems {
		val := 0.0
		switch sys.WorstStatus {
		case monitor.StatusUP:
			val = 1.0
		case monitor.StatusDegraded:
			val = 0.5
		}
		fmt.Fprintf(&b, "argus_system_up{id=%q,name=%q,group=%q} %.1f\n", sys.ID, sys.Name, sys.Group, val)
	}

	b.WriteString("# HELP argus_check_latency_seconds Last observed check latency in seconds\n")
	b.WriteString("# TYPE argus_check_latency_seconds gauge\n")
	for _, sys := range systems {
		if sys.DNS.Configured && sys.DNS.HasHistory {
			fmt.Fprintf(&b, "argus_check_latency_seconds{id=%q,kind=\"dns\"} %.6f\n", sys.ID, sys.DNS.LastResult.Latency.Seconds())
		}
		if sys.IPCheck.Configured && sys.IPCheck.HasHistory {
			fmt.Fprintf(&b, "argus_check_latency_seconds{id=%q,kind=\"ip\"} %.6f\n", sys.ID, sys.IPCheck.LastResult.Latency.Seconds())
		}
	}

	b.WriteString("# HELP argus_check_packet_loss_ratio Last observed ICMP packet loss percentage (0..100)\n")
	b.WriteString("# TYPE argus_check_packet_loss_ratio gauge\n")
	for _, sys := range systems {
		if sys.IPCheck.Configured && sys.IPCheck.HasHistory {
			fmt.Fprintf(&b, "argus_check_packet_loss_ratio{id=%q} %.2f\n", sys.ID, sys.IPCheck.LastResult.Loss)
		}
	}

	b.WriteString("# HELP argus_tls_cert_expiry_days Days remaining until TLS certificate expiration\n")
	b.WriteString("# TYPE argus_tls_cert_expiry_days gauge\n")
	for _, sys := range systems {
		if sys.DNS.Configured && sys.DNS.LastResult.HasTLS {
			fmt.Fprintf(&b, "argus_tls_cert_expiry_days{id=%q} %d\n", sys.ID, sys.DNS.LastResult.TLSDaysLeft)
		}
	}

	_, _ = w.Write([]byte(b.String()))
}

// Handler: GET /healthz
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.storage != nil {
		if err := s.storage.Ping(r.Context()); err != nil {
			http.Error(w, fmt.Sprintf("db unhealth: %v", err), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ViewModels for clean template bindings

type cardViewModel struct {
	ID                   string
	Name                 string
	Group                string
	URL                  string
	DisplayURL           string
	IP                   string
	DisplayIP            string
	IsIPHTTP             bool
	TCPPort              int
	WorstStatus          string
	WorstStatusLower     string
	DNSStatusLower       string
	IPStatusLower        string
	DNSLatencyMs         int64
	IPLatencyMs          int64
	Uptime1h             float64
	Diagnosis            string
	DownDurationStr      string
	LastCheckedFormatted string
	LastCheckedTimeOnly  string
	DNS                  monitor.CheckSnapshot
	IPCheck              monitor.CheckSnapshot
	Heartbeat            []heartbeatViewModel
	SparklinePoints      string
	HasSparkline         bool
	HasTLS               bool
	TLSDaysLeft          int
	TLSExpiringSoon      bool
	InMaintenance        bool
	JustChanged          bool
}

type heartbeatViewModel struct {
	StatusLower string
	Tooltip     string
}

type eventViewModel struct {
	Kind                string
	FromStatus          string
	ToStatus            string
	ToStatusLower       string
	Diagnosis           string
	Detail              string
	OccurredAtFormatted string
	DurationStr         string
}

type failedCheckViewModel struct {
	Kind               string
	StatusCode         int
	LatencyMs          int64
	ErrorCat           string
	ErrorMsg           string
	CheckedAtFormatted string
}

type timelineBucketViewModel struct {
	DNSStatus      string
	DNSStatusLower string
	IPStatus       string
	IPStatusLower  string
	TimeFormatted  string
}

func toCardViewModel(snap monitor.SystemSnapshot) cardViewModel {
	dnsLower := strings.ToLower(string(snap.DNS.Status))
	ipLower := strings.ToLower(string(snap.IPCheck.Status))
	worstLower := strings.ToLower(string(snap.WorstStatus))

	displayURL := snap.URL
	if u, err := url.Parse(snap.URL); err == nil && u.Host != "" {
		displayURL = u.Host
	}

	displayIP := snap.IP
	if snap.IsIPHTTP {
		if u, err := url.Parse(snap.IP); err == nil && u.Host != "" {
			displayIP = u.Host
		}
	}

	lastCheckStr := ""
	lastCheckTimeOnly := ""
	if !snap.LastCheckedAt.IsZero() {
		localTime := snap.LastCheckedAt.Local()
		lastCheckStr = localTime.Format("02/01/2006 15:04:05")
		lastCheckTimeOnly = localTime.Format("15:04:05")
	}

	hbVM := make([]heartbeatViewModel, len(snap.Heartbeat))
	for i, tick := range snap.Heartbeat {
		hbVM[i] = heartbeatViewModel{
			StatusLower: strings.ToLower(string(tick.Status)),
			Tooltip:     tick.Tooltip,
		}
	}

	downDurStr := ""
	if snap.DownDuration > 0 {
		downDurStr = formatDurationShort(snap.DownDuration)
	}

	sparkPoints := buildSparklinePoints(snap.LatencyHistory)

	hasTLS := snap.DNS.LastResult.HasTLS
	tlsDaysLeft := snap.DNS.LastResult.TLSDaysLeft
	if !hasTLS && snap.IPCheck.LastResult.HasTLS {
		hasTLS = true
		tlsDaysLeft = snap.IPCheck.LastResult.TLSDaysLeft
	}

	return cardViewModel{
		ID:                   snap.ID,
		Name:                 snap.Name,
		Group:                snap.Group,
		URL:                  snap.URL,
		DisplayURL:           displayURL,
		IP:                   snap.IP,
		DisplayIP:            displayIP,
		IsIPHTTP:             snap.IsIPHTTP,
		TCPPort:              snap.TCPPort,
		WorstStatus:          string(snap.WorstStatus),
		WorstStatusLower:     worstLower,
		DNSStatusLower:       dnsLower,
		IPStatusLower:        ipLower,
		DNSLatencyMs:         snap.DNS.LastResult.Latency.Milliseconds(),
		IPLatencyMs:          snap.IPCheck.LastResult.Latency.Milliseconds(),
		Uptime1h:             snap.Uptime1h,
		Diagnosis:            snap.Diagnosis,
		DownDurationStr:      downDurStr,
		LastCheckedFormatted: lastCheckStr,
		LastCheckedTimeOnly:  lastCheckTimeOnly,
		DNS:                  snap.DNS,
		IPCheck:              snap.IPCheck,
		Heartbeat:            hbVM,
		SparklinePoints:      sparkPoints,
		HasSparkline:         sparkPoints != "",
		HasTLS:               hasTLS,
		TLSDaysLeft:          tlsDaysLeft,
		TLSExpiringSoon:      hasTLS && tlsDaysLeft < 15,
		InMaintenance:        snap.InMaintenance,
		JustChanged:          snap.JustChanged,
	}
}

func buildSparklinePoints(latencies []int64) string {
	n := len(latencies)
	if n < 2 {
		return ""
	}
	minVal, maxVal := latencies[0], latencies[0]
	for _, v := range latencies[1:] {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	const width = 60.0
	const height = 14.0
	const padY = 1.0

	var b strings.Builder
	span := float64(maxVal - minVal)
	for i, v := range latencies {
		x := float64(i) / float64(n-1) * width
		var y float64
		if span == 0 {
			y = padY + height/2.0
		} else {
			norm := float64(v-minVal) / span
			y = padY + height - (norm * height)
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
	}
	return b.String()
}

func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
