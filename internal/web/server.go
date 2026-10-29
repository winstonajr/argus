package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sentinela/internal/config"
	"sentinela/internal/monitor"
	"sentinela/internal/storage"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

// Server handles HTTP presentation and HTMX endpoints.
type Server struct {
	cfg            *config.Config
	store          *monitor.Store
	storage        *storage.DB
	mux            *http.ServeMux
	indexTmpl      *template.Template
	cardTmpl       *template.Template
	statsTmpl      *template.Template
	historyTmpl    *template.Template
	iconsSVG       template.HTML
}

// NewServer initializes the HTTP routing and parses embedded templates.
func NewServer(cfg *config.Config, store *monitor.Store, db *storage.DB) (*Server, error) {
	s := &Server{
		cfg:     cfg,
		store:   store,
		storage: db,
		mux:     http.NewServeMux(),
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
	s.mux.HandleFunc("GET /systems/{id}/history", s.handleHistory)

	// API and Health
	s.mux.HandleFunc("GET /api/systems/{id}", s.handleAPISystem)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Handler: GET /
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()

	data := struct {
		Title      string
		IconsSVG   template.HTML
		HideSearch bool
		Systems    []monitor.SystemSnapshot
	}{
		Title:      "Painel de Operações",
		IconsSVG:   s.iconsSVG,
		HideSearch: false,
		Systems:    systems,
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
	stats := s.store.GetStats()

	lastCheckStr := "—"
	if !stats.LastCheck.IsZero() {
		lastCheckStr = stats.LastCheck.Format("15:04:05")
	}

	uptimeDur := time.Since(stats.UptimeStart).Truncate(time.Second)
	uptimeStr := formatDurationShort(uptimeDur)

	data := struct {
		UpCount                int
		DegradedCount          int
		DownCount              int
		Total                  int
		WorstStatus            string
		LastCheckFormatted     string
		UptimeDurationFormatted string
	}{
		UpCount:                stats.UpCount,
		DegradedCount:          stats.DegradedCount,
		DownCount:              stats.DownCount,
		Total:                  stats.Total,
		WorstStatus:            string(stats.WorstStatus),
		LastCheckFormatted:     lastCheckStr,
		UptimeDurationFormatted: uptimeStr,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.statsTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render stats: %v", err)
	}
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
	default:
		period = "24h"
		duration = 24 * time.Hour
		periodLabel = "Últimas 24 horas"
		bucketCount = 60
	}

	to := time.Now().UTC()
	from := to.Add(-duration)

	ctx := r.Context()
	events, err := s.storage.ListEvents(ctx, id, from, to, 100)
	if err != nil {
		log.Printf("[WARN] list events: %v", err)
	}

	failed, err := s.storage.ListFailedChecks(ctx, id, from, to, 50, 0)
	if err != nil {
		log.Printf("[WARN] list failed checks: %v", err)
	}

	buckets, err := s.storage.GetTimelineBuckets(ctx, id, from, to, bucketCount)
	if err != nil {
		log.Printf("[WARN] timeline buckets: %v", err)
	}

	stats, err := s.storage.GetSystemUptimeStats(ctx, id, from, to)
	if err != nil {
		log.Printf("[WARN] uptime stats: %v", err)
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

	data := struct {
		Title        string
		IconsSVG     template.HTML
		HideSearch   bool
		System       monitor.SystemSnapshot
		Period       string
		PeriodLabel  string
		Stats        storage.SystemHistoryStats
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
	URL                  string
	DisplayURL           string
	IP                   string
	TCPPort              int
	WorstStatus          string
	WorstStatusLower     string
	DNSStatusLower       string
	IPStatusLower        string
	DNSLatencyMs         int64
	IPLatencyMs          int64
	Diagnosis            string
	DownDurationStr      string
	LastCheckedFormatted string
	LastCheckedTimeOnly  string
	DNS                  monitor.CheckSnapshot
	IPCheck              monitor.CheckSnapshot
	Heartbeat            []heartbeatViewModel
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

	return cardViewModel{
		ID:                   snap.ID,
		Name:                 snap.Name,
		URL:                  snap.URL,
		DisplayURL:           displayURL,
		IP:                   snap.IP,
		TCPPort:              snap.TCPPort,
		WorstStatus:          string(snap.WorstStatus),
		WorstStatusLower:     worstLower,
		DNSStatusLower:       dnsLower,
		IPStatusLower:        ipLower,
		DNSLatencyMs:         snap.DNS.LastResult.Latency.Milliseconds(),
		IPLatencyMs:          snap.IPCheck.LastResult.Latency.Milliseconds(),
		Diagnosis:            snap.Diagnosis,
		DownDurationStr:      downDurStr,
		LastCheckedFormatted: lastCheckStr,
		LastCheckedTimeOnly:  lastCheckTimeOnly,
		DNS:                  snap.DNS,
		IPCheck:              snap.IPCheck,
		Heartbeat:            hbVM,
		JustChanged:          snap.JustChanged,
	}
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
