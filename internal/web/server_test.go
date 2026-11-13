package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/monitor"
	"argus/internal/storage"
)

func setupTestServer(t *testing.T) (*Server, *monitor.Store, *storage.DB) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_web.db")

	db, err := storage.Open(dbPath, 24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to open storage: %v", err)
	}

	cfg := &config.Config{
		Server: config.ServerConfig{Addr: ":8080"},
		Defaults: config.DefaultsConfig{
			Interval:        10 * time.Second,
			FailThreshold:   2,
			DegradedLatency: 500 * time.Millisecond,
		},
		Systems: []config.SystemConfig{
			{ID: "erp", Name: "ERP Corporativo", URL: "https://erp.local", IP: "10.0.0.1"},
			{ID: "portal", Name: "Portal Cliente", URL: "https://portal.local"},
		},
	}

	store := monitor.NewStore(cfg)
	server, err := NewServer(cfg, store, db)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	return server, store, db
}

func TestServer_Endpoints(t *testing.T) {
	server, store, db := setupTestServer(t)
	defer db.Close()

	// Seed one check result
	store.RecordDNSResult("erp", checker.Result{
		OK:         true,
		StatusCode: 200,
		Latency:    42 * time.Millisecond,
		CheckedAt:  time.Now(),
	})

	// 1. GET /
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET / returned %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "ERP Corporativo") {
		t.Errorf("expected body to contain 'ERP Corporativo'")
	}
	if !strings.Contains(body, "hx-get=\"/cards/erp\"") {
		t.Errorf("expected body to contain HTMX card trigger")
	}

	// 2. GET /cards/erp (partial HTMX card)
	reqCard := httptest.NewRequest(http.MethodGet, "/cards/erp", nil)
	rrCard := httptest.NewRecorder()
	server.ServeHTTP(rrCard, reqCard)

	if rrCard.Code != http.StatusOK {
		t.Errorf("GET /cards/erp returned %d, want 200", rrCard.Code)
	}
	cardBody := rrCard.Body.String()
	if !strings.Contains(cardBody, "card--up") && !strings.Contains(cardBody, "card--unknown") {
		t.Errorf("expected card class, got: %s", cardBody)
	}

	// 3. GET /cards/unknown (404)
	req404 := httptest.NewRequest(http.MethodGet, "/cards/nonexistent", nil)
	rr404 := httptest.NewRecorder()
	server.ServeHTTP(rr404, req404)
	if rr404.Code != http.StatusNotFound {
		t.Errorf("GET /cards/nonexistent returned %d, want 404", rr404.Code)
	}

	// 4. GET /stats
	reqStats := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rrStats := httptest.NewRecorder()
	server.ServeHTTP(rrStats, reqStats)

	if rrStats.Code != http.StatusOK {
		t.Errorf("GET /stats returned %d, want 200", rrStats.Code)
	}
	statsBody := rrStats.Body.String()
	if !strings.Contains(statsBody, "Operacionais") {
		t.Errorf("expected stats body to contain 'Operacionais'")
	}

	// 5. GET /systems/erp/history
	reqHist := httptest.NewRequest(http.MethodGet, "/systems/erp/history?period=24h", nil)
	rrHist := httptest.NewRecorder()
	server.ServeHTTP(rrHist, reqHist)

	if rrHist.Code != http.StatusOK {
		t.Errorf("GET /systems/erp/history returned %d, want 200", rrHist.Code)
	}
	histBody := rrHist.Body.String()
	if !strings.Contains(histBody, "Linha do Tempo") {
		t.Errorf("expected history body to contain 'Linha do Tempo'")
	}

	// 6. GET /api/systems/erp
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/systems/erp", nil)
	rrAPI := httptest.NewRecorder()
	server.ServeHTTP(rrAPI, reqAPI)

	if rrAPI.Code != http.StatusOK {
		t.Errorf("GET /api/systems/erp returned %d, want 200", rrAPI.Code)
	}
	if !strings.Contains(rrAPI.Body.String(), `"ID":"erp"`) {
		t.Errorf("expected json ID erp, got: %s", rrAPI.Body.String())
	}

	// 7. GET /healthz
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rrHealth := httptest.NewRecorder()
	server.ServeHTTP(rrHealth, reqHealth)

	if rrHealth.Code != http.StatusOK || rrHealth.Body.String() != "ok" {
		t.Errorf("GET /healthz returned %d (%s), want 200 ok", rrHealth.Code, rrHealth.Body.String())
	}

	// 8. Static file check (tokens.css)
	reqCSS := httptest.NewRequest(http.MethodGet, "/static/css/tokens.css", nil)
	rrCSS := httptest.NewRecorder()
	server.ServeHTTP(rrCSS, reqCSS)

	if rrCSS.Code != http.StatusOK {
		t.Errorf("GET /static/css/tokens.css returned %d, want 200", rrCSS.Code)
	}
}
