package web

import (
	"bufio"
	"context"
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
	if !strings.Contains(body, "id=\"card-erp\"") {
		t.Errorf("expected body to contain rendered card 'card-erp'")
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

func TestServer_CardDiagnosisSlot(t *testing.T) {
	server, store, db := setupTestServer(t)
	defer db.Close()

	// 1. Index page renders real diagnosis slot directly (unknown when no results yet)
	reqIndex := httptest.NewRequest(http.MethodGet, "/", nil)
	rrIndex := httptest.NewRecorder()
	server.ServeHTTP(rrIndex, reqIndex)
	if !strings.Contains(rrIndex.Body.String(), "card__diagnosis-slot--unknown") {
		t.Errorf("expected index to include unknown diagnosis slot")
	}

	// 2. UNKNOWN status card (portal has no check results yet)
	reqUnknown := httptest.NewRequest(http.MethodGet, "/cards/portal", nil)
	rrUnknown := httptest.NewRecorder()
	server.ServeHTTP(rrUnknown, reqUnknown)
	unknownBody := rrUnknown.Body.String()
	if !strings.Contains(unknownBody, "card__diagnosis-slot--unknown") {
		t.Errorf("expected portal card to have unknown diagnosis slot, got: %s", unknownBody)
	}

	// 3. UP status card
	store.RecordDNSResult("erp", checker.Result{
		OK:         true,
		StatusCode: 200,
		Latency:    25 * time.Millisecond,
		CheckedAt:  time.Now(),
	})
	store.RecordIPResult("erp", checker.Result{
		OK:        true,
		Latency:   10 * time.Millisecond,
		CheckedAt: time.Now(),
	})
	reqUP := httptest.NewRequest(http.MethodGet, "/cards/erp", nil)
	rrUP := httptest.NewRecorder()
	server.ServeHTTP(rrUP, reqUP)
	upBody := rrUP.Body.String()
	if !strings.Contains(upBody, "card__diagnosis-slot--up") {
		t.Errorf("expected erp card to have up diagnosis slot, got: %s", upBody)
	}
	if !strings.Contains(upBody, "Todos os alvos operacionais") {
		t.Errorf("expected erp card to contain 'Todos os alvos operacionais', got: %s", upBody)
	}

	// 4. DOWN status card with diagnosis
	store.RecordDNSResult("erp", checker.Result{
		OK:         false,
		StatusCode: 502,
		Error:      "Bad Gateway",
		ErrorCat:   checker.CatProxy,
		CheckedAt:  time.Now(),
	})
	store.RecordDNSResult("erp", checker.Result{
		OK:         false,
		StatusCode: 502,
		Error:      "Bad Gateway",
		ErrorCat:   checker.CatProxy,
		CheckedAt:  time.Now(),
	})
	reqDown := httptest.NewRequest(http.MethodGet, "/cards/erp", nil)
	rrDown := httptest.NewRecorder()
	server.ServeHTTP(rrDown, reqDown)
	downBody := rrDown.Body.String()
	if !strings.Contains(downBody, "card__diagnosis-slot--down") {
		t.Errorf("expected erp card to have down diagnosis slot, got: %s", downBody)
	}
	if !strings.Contains(downBody, "502") {
		t.Errorf("expected diagnosis to mention 502, got: %s", downBody)
	}

	// 5. DEGRADED status card
	// Recover to UP first
	store.RecordDNSResult("erp", checker.Result{
		OK:         true,
		StatusCode: 200,
		Latency:    30 * time.Millisecond,
		CheckedAt:  time.Now(),
	})
	store.RecordDNSResult("erp", checker.Result{
		OK:         true,
		StatusCode: 200,
		Latency:    30 * time.Millisecond,
		CheckedAt:  time.Now(),
	})
	// Single failure triggers DEGRADED state
	store.RecordDNSResult("erp", checker.Result{
		OK:         false,
		StatusCode: 503,
		Error:      "Service Unavailable",
		ErrorCat:   checker.CatHTTP5xx,
		CheckedAt:  time.Now(),
	})
	reqDegraded := httptest.NewRequest(http.MethodGet, "/cards/erp", nil)
	rrDegraded := httptest.NewRecorder()
	server.ServeHTTP(rrDegraded, reqDegraded)
	degradedBody := rrDegraded.Body.String()
	if !strings.Contains(degradedBody, "card__diagnosis-slot--degraded") {
		t.Errorf("expected erp card to have degraded diagnosis slot, got: %s", degradedBody)
	}
}

func TestServer_CardHeartbeatSparklineAndSSE(t *testing.T) {
	server, store, db := setupTestServer(t)
	defer db.Close()

	now := time.Now()
	store.RecordDNSResult("erp", checker.Result{OK: true, StatusCode: 200, Latency: 20 * time.Millisecond, CheckedAt: now})
	store.RecordDNSResult("erp", checker.Result{OK: true, StatusCode: 200, Latency: 40 * time.Millisecond, CheckedAt: now.Add(time.Second)})

	reqCard := httptest.NewRequest(http.MethodGet, "/cards/erp", nil)
	rrCard := httptest.NewRecorder()
	server.ServeHTTP(rrCard, reqCard)
	body := rrCard.Body.String()
	if !strings.Contains(body, "heartbeat-bar") {
		t.Errorf("expected card to render heartbeat-bar")
	}
	if !strings.Contains(body, `class="sparkline"`) {
		t.Errorf("expected card to render latency sparkline")
	}

	// Test SSE endpoint using real httptest.Server
	ts := httptest.NewServer(server)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE request failed: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	// Read initial ": connected" line
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "connected") {
		t.Fatalf("expected connected comment, got %q (err=%v)", line, err)
	}

	// Trigger a check update -> should emit event: card
	store.RecordDNSResult("erp", checker.Result{OK: true, StatusCode: 200, Latency: 25 * time.Millisecond, CheckedAt: time.Now()})

	foundCardEvent := false
	for i := 0; i < 10; i++ {
		l, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(l, "event: card") {
			foundCardEvent = true
			break
		}
	}
	if !foundCardEvent {
		t.Errorf("expected SSE stream to emit 'event: card'")
	}
}

func TestServer_Phase3Endpoints(t *testing.T) {
	server, store, db := setupTestServer(t)
	defer db.Close()

	store.RecordDNSResult("erp", checker.Result{
		OK:          true,
		StatusCode:  200,
		Latency:     35 * time.Millisecond,
		HasTLS:      true,
		TLSDaysLeft: 42,
		CheckedAt:   time.Now(),
	})

	// 1. GET /status (public status page - must not expose internal IP)
	reqStatus := httptest.NewRequest(http.MethodGet, "/status", nil)
	rrStatus := httptest.NewRecorder()
	server.ServeHTTP(rrStatus, reqStatus)
	if rrStatus.Code != http.StatusOK {
		t.Errorf("GET /status returned %d, want 200", rrStatus.Code)
	}
	statusBody := rrStatus.Body.String()
	if !strings.Contains(statusBody, "ERP Corporativo") {
		t.Errorf("expected /status to contain 'ERP Corporativo'")
	}
	if strings.Contains(statusBody, "10.0.0.1") {
		t.Errorf("expected /status NOT to expose internal IP 10.0.0.1")
	}

	// 2. GET /api/systems
	reqAPISys := httptest.NewRequest(http.MethodGet, "/api/systems", nil)
	rrAPISys := httptest.NewRecorder()
	server.ServeHTTP(rrAPISys, reqAPISys)
	if rrAPISys.Code != http.StatusOK || !strings.Contains(rrAPISys.Body.String(), `"erp"`) {
		t.Errorf("GET /api/systems unexpected response: %d %s", rrAPISys.Code, rrAPISys.Body.String())
	}

	// 3. GET /api/events
	reqAPIEv := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rrAPIEv := httptest.NewRecorder()
	server.ServeHTTP(rrAPIEv, reqAPIEv)
	if rrAPIEv.Code != http.StatusOK {
		t.Errorf("GET /api/events returned %d, want 200", rrAPIEv.Code)
	}

	// 4. GET /metrics (Prometheus)
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rrMetrics := httptest.NewRecorder()
	server.ServeHTTP(rrMetrics, reqMetrics)
	if rrMetrics.Code != http.StatusOK {
		t.Errorf("GET /metrics returned %d, want 200", rrMetrics.Code)
	}
	metricsBody := rrMetrics.Body.String()
	if !strings.Contains(metricsBody, `argus_system_up{id="erp"`) {
		t.Errorf("expected Prometheus argus_system_up metric, got: %s", metricsBody)
	}
	if !strings.Contains(metricsBody, `argus_tls_cert_expiry_days{id="erp"} 42`) {
		t.Errorf("expected Prometheus TLS expiry metric, got: %s", metricsBody)
	}

	// 5. GET /systems/erp/export?period=30d&format=csv
	reqExport := httptest.NewRequest(http.MethodGet, "/systems/erp/export?period=30d&format=csv", nil)
	rrExport := httptest.NewRecorder()
	server.ServeHTTP(rrExport, reqExport)
	if rrExport.Code != http.StatusOK || !strings.Contains(rrExport.Body.String(), "occurred_at,kind") {
		t.Errorf("GET /systems/erp/export CSV failed: %d %s", rrExport.Code, rrExport.Body.String())
	}
}

func TestServer_MaintenanceAndNilStorage(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 2, DegradedLatency: 2 * time.Second},
		Systems: []config.SystemConfig{
			{ID: "erp", Name: "ERP", URL: "https://erp.local"},
		},
	}
	store := monitor.NewStore(cfg)
	server, err := NewServer(cfg, store, nil)
	if err != nil {
		t.Fatalf("NewServer with nil storage failed: %v", err)
	}

	// 1. Toggle maintenance via POST
	reqMaint := httptest.NewRequest(http.MethodPost, "/api/systems/erp/maintenance?enabled=true", nil)
	rrMaint := httptest.NewRecorder()
	server.ServeHTTP(rrMaint, reqMaint)
	if rrMaint.Code != http.StatusOK || !strings.Contains(rrMaint.Body.String(), `"in_maintenance":true`) {
		t.Fatalf("POST maintenance unexpected: %d %s", rrMaint.Code, rrMaint.Body.String())
	}

	// Card should display MANUTENÇÃO badge
	reqCard := httptest.NewRequest(http.MethodGet, "/cards/erp", nil)
	rrCard := httptest.NewRecorder()
	server.ServeHTTP(rrCard, reqCard)
	if !strings.Contains(rrCard.Body.String(), "MANUTENÇÃO") {
		t.Errorf("expected card to display MANUTENÇÃO badge")
	}

	// 2. Nil storage must not panic on /api/events, /systems/erp/history, /systems/erp/export
	for _, path := range []string{"/api/events", "/systems/erp/history", "/systems/erp/export?format=json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s with nil storage returned %d, want 200", path, rr.Code)
		}
	}
}
