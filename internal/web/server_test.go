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
	"argus/internal/notify"
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

func TestServer_HistoryStatusBannerAndCardWithoutDiagnosisSlot(t *testing.T) {
	server, store, db := setupTestServer(t)
	defer db.Close()

	// 1. Index page and card do not render diagnosis slot
	reqIndex := httptest.NewRequest(http.MethodGet, "/", nil)
	rrIndex := httptest.NewRecorder()
	server.ServeHTTP(rrIndex, reqIndex)
	if strings.Contains(rrIndex.Body.String(), "card__diagnosis-slot") {
		t.Errorf("expected index card not to include diagnosis slot")
	}

	// 2. UNKNOWN status on history page (portal has no check results yet)
	reqCardUnknown := httptest.NewRequest(http.MethodGet, "/cards/portal", nil)
	rrCardUnknown := httptest.NewRecorder()
	server.ServeHTTP(rrCardUnknown, reqCardUnknown)
	if strings.Contains(rrCardUnknown.Body.String(), "card__diagnosis-slot") {
		t.Errorf("expected portal card not to include diagnosis slot")
	}

	reqUnknown := httptest.NewRequest(http.MethodGet, "/systems/portal/history", nil)
	rrUnknown := httptest.NewRecorder()
	server.ServeHTTP(rrUnknown, reqUnknown)
	unknownBody := rrUnknown.Body.String()
	if !strings.Contains(unknownBody, "history-status-banner--unknown") {
		t.Errorf("expected portal history to have unknown status banner, got: %s", unknownBody)
	}

	// 3. UP status on history page
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
	reqUP := httptest.NewRequest(http.MethodGet, "/systems/erp/history", nil)
	rrUP := httptest.NewRecorder()
	server.ServeHTTP(rrUP, reqUP)
	upBody := rrUP.Body.String()
	if !strings.Contains(upBody, "history-status-banner--up") {
		t.Errorf("expected erp history to have up status banner, got: %s", upBody)
	}
	if !strings.Contains(upBody, "Todos os alvos operacionais") {
		t.Errorf("expected erp history to contain 'Todos os alvos operacionais', got: %s", upBody)
	}

	// 4. DOWN status with diagnosis on history page
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
	reqDown := httptest.NewRequest(http.MethodGet, "/systems/erp/history", nil)
	rrDown := httptest.NewRecorder()
	server.ServeHTTP(rrDown, reqDown)
	downBody := rrDown.Body.String()
	if !strings.Contains(downBody, "history-status-banner--down") {
		t.Errorf("expected erp history to have down status banner, got: %s", downBody)
	}
	if !strings.Contains(downBody, "Aplicação fora (borda Cloudflare)") {
		t.Errorf("expected erp history diagnosis to mention 'Aplicação fora (borda Cloudflare)', got: %s", downBody)
	}

	// 5. DEGRADED status on history page
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
	reqDegraded := httptest.NewRequest(http.MethodGet, "/systems/erp/history", nil)
	rrDegraded := httptest.NewRecorder()
	server.ServeHTTP(rrDegraded, reqDegraded)
	degradedBody := rrDegraded.Body.String()
	if !strings.Contains(degradedBody, "history-status-banner--degraded") {
		t.Errorf("expected erp history to have degraded status banner, got: %s", degradedBody)
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

func TestServer_TriggerCheckAndUIControls(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 2, DegradedLatency: 2 * time.Second},
		Systems: []config.SystemConfig{
			{ID: "erp", Name: "ERP", URL: "https://erp.local"},
		},
	}
	store := monitor.NewStore(cfg)
	server, err := NewServer(cfg, store, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Without trigger configured -> 503
	reqUnavail := httptest.NewRequest(http.MethodPost, "/api/systems/erp/check", nil)
	rrUnavail := httptest.NewRecorder()
	server.ServeHTTP(rrUnavail, reqUnavail)
	if rrUnavail.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when trigger unavailable, got %d", rrUnavail.Code)
	}

	// 2. Configure trigger
	triggered := 0
	server.SetCheckTrigger(func(sysID string) bool {
		if sysID == "erp" {
			triggered++
			return true
		}
		return false
	})

	// Unknown system -> 404
	req404 := httptest.NewRequest(http.MethodPost, "/api/systems/unknown/check", nil)
	rr404 := httptest.NewRecorder()
	server.ServeHTTP(rr404, req404)
	if rr404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown system, got %d", rr404.Code)
	}

	// Valid system -> 202 Accepted
	reqOK := httptest.NewRequest(http.MethodPost, "/api/systems/erp/check", nil)
	rrOK := httptest.NewRecorder()
	server.ServeHTTP(rrOK, reqOK)
	if rrOK.Code != http.StatusAccepted || triggered != 1 {
		t.Fatalf("expected 202 and triggered=1, got %d (triggered=%d)", rrOK.Code, triggered)
	}

	// Immediate retry within 3s -> 429 Too Many Requests
	req429 := httptest.NewRequest(http.MethodPost, "/api/systems/erp/check", nil)
	rr429 := httptest.NewRecorder()
	server.ServeHTTP(rr429, req429)
	if rr429.Code != http.StatusTooManyRequests || triggered != 1 {
		t.Fatalf("expected 429 rate limit and triggered=1, got %d (triggered=%d)", rr429.Code, triggered)
	}

	// 3. UI controls presence
	reqIndex := httptest.NewRequest(http.MethodGet, "/", nil)
	rrIndex := httptest.NewRecorder()
	server.ServeHTTP(rrIndex, reqIndex)
	if !strings.Contains(rrIndex.Body.String(), `id="sort-toggle-btn"`) {
		t.Errorf("expected index page to include sort-toggle-btn")
	}

	reqHist := httptest.NewRequest(http.MethodGet, "/systems/erp/history", nil)
	rrHist := httptest.NewRecorder()
	server.ServeHTTP(rrHist, reqHist)
	histBody := rrHist.Body.String()
	if !strings.Contains(histBody, `id="maintenance-toggle-btn"`) {
		t.Errorf("expected history page to include maintenance-toggle-btn")
	}
	if !strings.Contains(histBody, `id="check-now-btn"`) {
		t.Errorf("expected history page to include check-now-btn")
	}

	// 4. Test Alert endpoint & manifest/favicon links
	server.SetNotifier(notify.New(config.AlertsConfig{}))
	reqAlert := httptest.NewRequest(http.MethodPost, "/api/alerts/test", nil)
	rrAlert := httptest.NewRecorder()
	server.ServeHTTP(rrAlert, reqAlert)
	if rrAlert.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when no alert channels configured, got %d", rrAlert.Code)
	}

	reqFav := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	rrFav := httptest.NewRecorder()
	server.ServeHTTP(rrFav, reqFav)
	if rrFav.Code != http.StatusOK {
		t.Errorf("expected 200 for /favicon.ico, got %d", rrFav.Code)
	}

	if !strings.Contains(rrIndex.Body.String(), `rel="manifest"`) {
		t.Errorf("expected index page to include PWA manifest link")
	}
}

func TestServer_StaticCacheGzipCSRFAndI18n(t *testing.T) {
	server, _, db := setupTestServer(t)
	defer db.Close()

	// 1. Static asset with ?v= returns immutable Cache-Control, ETag, and gzip when requested
	reqStatic := httptest.NewRequest(http.MethodGet, "/static/css/app.css?v=test", nil)
	reqStatic.Header.Set("Accept-Encoding", "gzip")
	rrStatic := httptest.NewRecorder()
	server.ServeHTTP(rrStatic, reqStatic)

	if rrStatic.Code != http.StatusOK {
		t.Fatalf("GET /static/css/app.css returned %d, want 200", rrStatic.Code)
	}
	if !strings.Contains(rrStatic.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("expected immutable Cache-Control, got %q", rrStatic.Header().Get("Cache-Control"))
	}
	if rrStatic.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", rrStatic.Header().Get("Content-Encoding"))
	}
	etag := rrStatic.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("expected non-empty ETag header")
	}

	// 304 Not Modified on matching If-None-Match
	req304 := httptest.NewRequest(http.MethodGet, "/static/css/app.css?v=test", nil)
	req304.Header.Set("If-None-Match", etag)
	rr304 := httptest.NewRecorder()
	server.ServeHTTP(rr304, req304)
	if rr304.Code != http.StatusNotModified {
		t.Errorf("expected 304 Not Modified, got %d", rr304.Code)
	}

	// 2. Cross-origin POST blocked by CSRF protection (403)
	reqCSRF := httptest.NewRequest(http.MethodPost, "/api/systems/erp/maintenance", nil)
	reqCSRF.Header.Set("Sec-Fetch-Site", "cross-site")
	rrCSRF := httptest.NewRecorder()
	server.ServeHTTP(rrCSRF, reqCSRF)
	if rrCSRF.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on cross-site POST, got %d", rrCSRF.Code)
	}

	// 3. History page empty buckets rendered as timeline-block--unknown (not UP)
	reqHist := httptest.NewRequest(http.MethodGet, "/systems/portal/history?period=1h", nil)
	rrHist := httptest.NewRecorder()
	server.ServeHTTP(rrHist, reqHist)
	if !strings.Contains(rrHist.Body.String(), "timeline-block--unknown") {
		t.Errorf("expected empty timeline buckets to render timeline-block--unknown")
	}

	// 4. Language switch via ?lang=en
	reqEN := httptest.NewRequest(http.MethodGet, "/?lang=en", nil)
	rrEN := httptest.NewRecorder()
	server.ServeHTTP(rrEN, reqEN)
	if !strings.Contains(rrEN.Body.String(), "Operations Console") {
		t.Errorf("expected English title 'Operations Console' on /?lang=en")
	}
	if strings.Contains(rrEN.Body.String(), "htmx.min.js") {
		t.Errorf("expected HTMX script reference to be removed")
	}
	if !strings.Contains(rrEN.Body.String(), `data-santa-easter-egg="`) {
		t.Errorf("expected body to include data-santa-easter-egg attribute")
	}

	// 5. GET /api/cards returns stats_html and cards array
	reqCards := httptest.NewRequest(http.MethodGet, "/api/cards", nil)
	rrCards := httptest.NewRecorder()
	server.ServeHTTP(rrCards, reqCards)
	if rrCards.Code != http.StatusOK || !strings.Contains(rrCards.Body.String(), `"stats_html"`) {
		t.Errorf("GET /api/cards failed: %d %s", rrCards.Code, rrCards.Body.String())
	}

	// 6. GET /api/systems/erp/failed-checks
	reqFailed := httptest.NewRequest(http.MethodGet, "/api/systems/erp/failed-checks?period=24h&offset=0", nil)
	rrFailed := httptest.NewRecorder()
	server.ServeHTTP(rrFailed, reqFailed)
	if rrFailed.Code != http.StatusOK || !strings.Contains(rrFailed.Body.String(), `"has_more"`) {
		t.Errorf("GET /api/systems/erp/failed-checks failed: %d %s", rrFailed.Code, rrFailed.Body.String())
	}
}

func TestServer_AuthMiddlewareAndExpiringMaintenance(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr: ":8080",
			Auth: config.AuthConfig{
				Username: "noc",
				Password: "secret-password",
				Token:    "api-secret-token",
			},
		},
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 2, DegradedLatency: 2 * time.Second},
		Systems: []config.SystemConfig{
			{ID: "erp", Name: "ERP", URL: "https://erp.local"},
		},
	}
	store := monitor.NewStore(cfg)
	server, err := NewServer(cfg, store, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Public routes remain accessible without credentials
	for _, pubPath := range []string{"/status", "/healthz", "/metrics", "/favicon.ico", "/static/css/tokens.css"} {
		req := httptest.NewRequest(http.MethodGet, pubPath, nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected public route %s to return 200 without auth, got %d", pubPath, rr.Code)
		}
	}

	// 2. Protected routes return 401 without credentials
	for _, protPath := range []string{"/", "/api/systems", "/systems/erp/history"} {
		req := httptest.NewRequest(http.MethodGet, protPath, nil)
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected protected route %s to return 401 without auth, got %d", protPath, rr.Code)
		}
	}

	// 3. Basic Auth grants access
	reqBasic := httptest.NewRequest(http.MethodGet, "/", nil)
	reqBasic.SetBasicAuth("noc", "secret-password")
	rrBasic := httptest.NewRecorder()
	server.ServeHTTP(rrBasic, reqBasic)
	if rrBasic.Code != http.StatusOK {
		t.Errorf("expected 200 with valid Basic Auth, got %d", rrBasic.Code)
	}

	// 4. Bearer token grants access & maintenance with duration works
	reqMaint := httptest.NewRequest(http.MethodPost, "/api/systems/erp/maintenance?enabled=true&duration=1h", nil)
	reqMaint.Header.Set("Authorization", "Bearer api-secret-token")
	rrMaint := httptest.NewRecorder()
	server.ServeHTTP(rrMaint, reqMaint)
	if rrMaint.Code != http.StatusOK || !strings.Contains(rrMaint.Body.String(), `"until"`) {
		t.Errorf("expected 200 with until timestamp on maintenance POST, got %d %s", rrMaint.Code, rrMaint.Body.String())
	}
}

func TestServer_JSONErrorsAndCSVSanitization(t *testing.T) {
	server, _, db := setupTestServer(t)
	defer db.Close()

	req404 := httptest.NewRequest(http.MethodGet, "/api/systems/nonexistent", nil)
	rr404 := httptest.NewRecorder()
	server.ServeHTTP(rr404, req404)
	if rr404.Code != http.StatusNotFound || !strings.HasPrefix(rr404.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected 404 application/json, got %d %q", rr404.Code, rr404.Header().Get("Content-Type"))
	}

	reqBadMaint := httptest.NewRequest(http.MethodPost, "/api/systems/erp/maintenance?enabled=true&duration=invalid", nil)
	rrBadMaint := httptest.NewRecorder()
	server.ServeHTTP(rrBadMaint, reqBadMaint)
	if rrBadMaint.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid maintenance duration, got %d", rrBadMaint.Code)
	}

	if got := sanitizeCSVCell("=CMD('calc')"); got != "'=CMD('calc')" {
		t.Errorf("expected CSV formula prefix neutralized, got %q", got)
	}
	if got := sanitizeCSVCell("normal text"); got != "normal text" {
		t.Errorf("expected normal CSV cell untouched, got %q", got)
	}
}
