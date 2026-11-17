package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"sync"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/i18n"
	"argus/internal/monitor"
	"argus/internal/notify"
	"argus/internal/storage"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

// Server handles HTTP presentation, SSE, and API endpoints.
type Server struct {
	cfgMu           sync.RWMutex
	cfg             *config.Config
	store           *monitor.Store
	storage         *storage.DB
	mux             *http.ServeMux
	handler         http.Handler
	assets          map[string]cachedAsset
	indexTmpl       *template.Template
	cardTmpl        *template.Template
	statsTmpl       *template.Template
	historyTmpl     *template.Template
	statusTmpl      *template.Template
	iconsSVG        template.HTML
	sseMu           sync.RWMutex
	sseClients      map[chan sseEvent]struct{}
	triggerMu       sync.RWMutex
	checkTrigger    func(string) bool
	notifierMu      sync.RWMutex
	notifier        *notify.Notifier
	manualCheckMu   sync.Mutex
	lastManualCheck map[string]time.Time
}

// NewServer initializes the HTTP routing, static asset cache, and embedded templates.
func NewServer(cfg *config.Config, store *monitor.Store, db *storage.DB) (*Server, error) {
	if cfg != nil && cfg.Server.Lang != "" {
		i18n.SetDefault(cfg.Server.Lang)
	}

	s := &Server{
		cfg:             cfg,
		store:           store,
		storage:         db,
		mux:             http.NewServeMux(),
		sseClients:      make(map[chan sseEvent]struct{}),
		lastManualCheck: make(map[string]time.Time),
	}

	if err := s.initStaticAssets(); err != nil {
		return nil, fmt.Errorf("init static assets: %w", err)
	}

	iconsData, err := staticFS.ReadFile("static/icons.svg")
	if err != nil {
		return nil, fmt.Errorf("read icons.svg: %w", err)
	}
	s.iconsSVG = template.HTML(iconsData) //nolint:gosec

	if err := s.parseTemplates(); err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s.routes()

	cop := http.NewCrossOriginProtection()
	s.handler = s.withSecurityHeaders(cop.Handler(s.mux))

	if store != nil {
		store.SubscribeChecks(func(sysID string, kind string, res checker.Result) {
			s.broadcastSystemUpdate(sysID)
		})
	}

	return s, nil
}

func (s *Server) parseTemplates() error {
	funcs := template.FuncMap{
		"asset": s.assetURL,
	}

	parse := func(files ...string) (*template.Template, error) {
		return template.New(path.Base(files[0])).Funcs(funcs).ParseFS(templateFS, files...)
	}

	var err error
	s.indexTmpl, err = parse("templates/layout.html", "templates/index.html")
	if err != nil {
		return fmt.Errorf("parse index tmpl: %w", err)
	}

	s.cardTmpl, err = template.New("card.html").Funcs(funcs).ParseFS(templateFS, "templates/card.html")
	if err != nil {
		return fmt.Errorf("parse card tmpl: %w", err)
	}

	s.statsTmpl, err = template.New("stats.html").Funcs(funcs).ParseFS(templateFS, "templates/stats.html")
	if err != nil {
		return fmt.Errorf("parse stats tmpl: %w", err)
	}

	s.historyTmpl, err = parse("templates/layout.html", "templates/history.html")
	if err != nil {
		return fmt.Errorf("parse history tmpl: %w", err)
	}

	s.statusTmpl, err = parse("templates/layout.html", "templates/status.html")
	if err != nil {
		return fmt.Errorf("parse status tmpl: %w", err)
	}

	return nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /static/", s.handleStatic)
	s.mux.HandleFunc("GET /favicon.ico", s.handleFavicon)

	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /cards/{id}", s.handleCard)
	s.mux.HandleFunc("GET /stats", s.handleStats)
	s.mux.HandleFunc("GET /events", s.handleSSE)
	s.mux.HandleFunc("GET /status", s.handlePublicStatus)
	s.mux.HandleFunc("GET /systems/{id}/history", s.handleHistory)
	s.mux.HandleFunc("GET /systems/{id}/export", s.handleExport)

	s.mux.HandleFunc("GET /api/systems", s.handleAPISystems)
	s.mux.HandleFunc("GET /api/systems/{id}", s.handleAPISystem)
	s.mux.HandleFunc("POST /api/systems/{id}/check", s.handleTriggerCheck)
	s.mux.HandleFunc("POST /api/systems/{id}/maintenance", s.handleToggleMaintenance)
	s.mux.HandleFunc("POST /api/alerts/test", s.handleTestAlert)
	s.mux.HandleFunc("GET /api/events", s.handleAPIEvents)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
}

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) defaultMessages() i18n.Messages {
	s.cfgMu.RLock()
	var lang string
	if s.cfg != nil {
		lang = s.cfg.Server.Lang
	}
	s.cfgMu.RUnlock()
	if lang != "" {
		return i18n.For(lang)
	}
	return i18n.Current()
}

func (s *Server) messagesForRequest(w http.ResponseWriter, r *http.Request) i18n.Messages {
	if q := r.URL.Query().Get("lang"); q != "" {
		norm := i18n.Normalize(q)
		http.SetCookie(w, &http.Cookie{
			Name:     "argus-lang",
			Value:    norm,
			Path:     "/",
			MaxAge:   365 * 86400,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
		})
		return i18n.For(norm)
	}
	if c, err := r.Cookie("argus-lang"); err == nil && c.Value != "" {
		return i18n.For(c.Value)
	}
	return s.defaultMessages()
}

// SetCheckTrigger registers a callback to execute an immediate system check.
func (s *Server) SetCheckTrigger(fn func(string) bool) {
	s.triggerMu.Lock()
	s.checkTrigger = fn
	s.triggerMu.Unlock()
}

// SetNotifier registers the notifier instance used for alert testing.
func (s *Server) SetNotifier(n *notify.Notifier) {
	s.notifierMu.Lock()
	s.notifier = n
	s.notifierMu.Unlock()
}

// UpdateConfig updates the server's configuration reference safely at runtime.
func (s *Server) UpdateConfig(cfg *config.Config) {
	s.cfgMu.Lock()
	s.cfg = cfg
	if cfg != nil && cfg.Server.Lang != "" {
		i18n.SetDefault(cfg.Server.Lang)
	}
	s.cfgMu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
