package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
)

func TestStore_StateMachine(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:        10 * time.Second,
			FailThreshold:   2,
			DegradedLatency: 100 * time.Millisecond,
		},
		Systems: []config.SystemConfig{
			{ID: "app-1", Name: "App One", URL: "http://example.com", IP: "1.1.1.1"},
		},
	}

	store := NewStore(cfg)

	events := make([]ChangeEvent, 0)
	store.SubscribeEvents(func(e ChangeEvent) {
		events = append(events, e)
	})

	now := time.Now()

	// 1. Initial successful check -> UP
	store.RecordDNSResult("app-1", checker.Result{
		OK:        true,
		Latency:   10 * time.Millisecond,
		CheckedAt: now,
	})

	snap, _ := store.GetSystem("app-1")
	if snap.DNS.Status != StatusUP {
		t.Fatalf("expected UP, got %v", snap.DNS.Status)
	}

	// 2. High latency check -> DEGRADED
	store.RecordDNSResult("app-1", checker.Result{
		OK:        true,
		Latency:   200 * time.Millisecond,
		CheckedAt: now.Add(1 * time.Second),
	})
	snap, _ = store.GetSystem("app-1")
	if snap.DNS.Status != StatusDegraded {
		t.Fatalf("expected DEGRADED due to latency, got %v", snap.DNS.Status)
	}

	// 3. First failure -> DEGRADED (consecFails = 1 < failThreshold 2)
	store.RecordDNSResult("app-1", checker.Result{
		OK:        false,
		ErrorCat:  checker.CatHTTP5xx,
		Error:     "500 Internal Error",
		CheckedAt: now.Add(2 * time.Second),
	})
	snap, _ = store.GetSystem("app-1")
	if snap.DNS.Status != StatusDegraded {
		t.Fatalf("expected DEGRADED after 1 failure, got %v", snap.DNS.Status)
	}

	// 4. Second consecutive failure -> DOWN
	store.RecordDNSResult("app-1", checker.Result{
		OK:        false,
		ErrorCat:  checker.CatHTTP5xx,
		Error:     "500 Internal Error",
		CheckedAt: now.Add(3 * time.Second),
	})
	snap, _ = store.GetSystem("app-1")
	if snap.DNS.Status != StatusDown {
		t.Fatalf("expected DOWN after 2 consecutive failures, got %v", snap.DNS.Status)
	}

	// 5. Recovery check -> UP
	store.RecordDNSResult("app-1", checker.Result{
		OK:        true,
		Latency:   10 * time.Millisecond,
		CheckedAt: now.Add(4 * time.Second),
	})
	snap, _ = store.GetSystem("app-1")
	if snap.DNS.Status != StatusUP {
		t.Fatalf("expected UP after recovery, got %v", snap.DNS.Status)
	}

	if len(events) < 4 {
		t.Errorf("expected at least 4 change events, got %d", len(events))
	}
}

func TestStore_Diagnosis(t *testing.T) {
	tests := []struct {
		name      string
		dnsResult checker.Result
		ipResult  checker.Result
		wantDiag  string
	}{
		{
			name:      "Cloudflare error while IP is alive",
			dnsResult: checker.Result{OK: false, ErrorCat: checker.CatProxy, Error: "Cloudflare 502"},
			ipResult:  checker.Result{OK: true, Latency: 2 * time.Millisecond},
			wantDiag:  "Aplicação fora (borda Cloudflare)",
		},
		{
			name:      "DNS fail while IP is alive",
			dnsResult: checker.Result{OK: false, ErrorCat: checker.CatDNS, Error: "no such host"},
			ipResult:  checker.Result{OK: true, Latency: 2 * time.Millisecond},
			wantDiag:  "Problema de DNS",
		},
		{
			name:      "Both DNS and IP fail",
			dnsResult: checker.Result{OK: false, ErrorCat: checker.CatTimeout},
			ipResult:  checker.Result{OK: false, ErrorCat: checker.CatICMPTimeout},
			wantDiag:  "VM ou rede fora",
		},
		{
			name:      "DNS UP but IP DOWN",
			dnsResult: checker.Result{OK: true, Latency: 20 * time.Millisecond},
			ipResult:  checker.Result{OK: false, ErrorCat: checker.CatICMPTimeout},
			wantDiag:  "Ping falhando",
		},
		{
			name:      "DNS UP but HTTP Host DOWN with status code",
			dnsResult: checker.Result{OK: true, Latency: 20 * time.Millisecond},
			ipResult:  checker.Result{OK: false, StatusCode: 500, ErrorCat: checker.CatHTTP5xx},
			wantDiag:  "Host interno com erro",
		},
		{
			name:      "DNS UP but HTTP Host connection refused",
			dnsResult: checker.Result{OK: true, Latency: 20 * time.Millisecond},
			ipResult:  checker.Result{OK: false, ErrorCat: checker.CatConnect, Error: "connection refused"},
			wantDiag:  "Host interno inacessível",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 1},
				Systems:  []config.SystemConfig{{ID: "s1", URL: "http://s1", IP: "1.1.1.1"}},
			}
			store := NewStore(cfg)
			store.RecordDNSResult("s1", tt.dnsResult)
			store.RecordIPResult("s1", tt.ipResult)

			snap, _ := store.GetSystem("s1")
			if snap.Diagnosis != tt.wantDiag {
				t.Errorf("got diagnosis %q, want %q", snap.Diagnosis, tt.wantDiag)
			}
		})
	}
}

func TestScheduler_StartStop(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 1},
		Systems: []config.SystemConfig{
			{ID: "dummy", Name: "Dummy", IP: "127.0.0.1", TCPPort: 65534},
		},
	}
	store := NewStore(cfg)
	httpChecker := checker.NewHTTPChecker()
	pingChecker := checker.NewPingChecker()

	sched := NewScheduler(cfg, store, httpChecker, pingChecker)
	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)

	// Wait briefly and cancel
	time.Sleep(50 * time.Millisecond)
	cancel()
	sched.Stop()
}

func TestScheduler_ExecuteIP_HTTP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	sysCfg := config.SystemConfig{
		ID:   "s-http-host",
		Name: "HTTP Host Test",
		IP:   ts.URL,
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 1},
		Systems:  []config.SystemConfig{sysCfg},
	}
	_ = cfg.Validate()

	store := NewStore(cfg)
	httpChecker := checker.NewHTTPChecker()
	pingChecker := checker.NewPingChecker()
	sched := NewScheduler(cfg, store, httpChecker, pingChecker)

	sched.executeIP(context.Background(), cfg.Systems[0])

	snap, ok := store.GetSystem("s-http-host")
	if !ok {
		t.Fatalf("system not found in store")
	}
	if !snap.IPCheck.LastResult.OK {
		t.Fatalf("expected IP check to succeed, got error: %s", snap.IPCheck.LastResult.Error)
	}
	if snap.IPCheck.Status != StatusUP {
		t.Errorf("expected StatusUP, got %s", snap.IPCheck.Status)
	}
}

func TestStore_Uptime1hAndHydration(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:        10 * time.Second,
			FailThreshold:   2,
			DegradedLatency: 500 * time.Millisecond,
		},
		Systems: []config.SystemConfig{
			{ID: "ip-only", Name: "IP Only", IP: "10.0.0.5", TCPPort: 5432},
			{ID: "dual", Name: "Dual", URL: "https://dual.local", IP: "10.0.0.6"},
		},
	}

	store := NewStore(cfg)

	eventFired := false
	store.SubscribeEvents(func(e ChangeEvent) {
		eventFired = true
	})

	now := time.Now()
	// Old check (> 1h ago) should not count toward 1h uptime
	oldFail := checker.Result{OK: false, Error: "timeout", CheckedAt: now.Add(-2 * time.Hour)}
	recentOK := checker.Result{OK: true, Latency: 15 * time.Millisecond, CheckedAt: now.Add(-10 * time.Minute)}
	recentFail := checker.Result{OK: false, Error: "refused", CheckedAt: now.Add(-5 * time.Minute)}

	store.HydrateCheck("ip-only", "ip", []checker.Result{oldFail, recentOK, recentFail}, now.Add(-5*time.Minute))
	if eventFired {
		t.Fatalf("HydrateCheck must not fire event subscribers")
	}

	snap, ok := store.GetSystem("ip-only")
	if !ok {
		t.Fatalf("ip-only not found")
	}
	if snap.Uptime1h != 50.0 {
		t.Errorf("expected ip-only Uptime1h = 50.0, got %.2f", snap.Uptime1h)
	}
	if snap.IPCheck.Status != StatusDegraded {
		t.Errorf("expected ip-only status DEGRADED (1 fail < threshold 2), got %s", snap.IPCheck.Status)
	}

	// Dual system: DNS 100%, IP 50% -> worst uptime must be 50%
	store.HydrateCheck("dual", "dns", []checker.Result{recentOK, recentOK}, time.Time{})
	store.HydrateCheck("dual", "ip", []checker.Result{recentOK, recentFail}, time.Time{})
	dualSnap, _ := store.GetSystem("dual")
	if dualSnap.Uptime1h != 50.0 {
		t.Errorf("expected dual Uptime1h = 50.0 (worst of DNS/IP), got %.2f", dualSnap.Uptime1h)
	}
}

func TestStore_MaintenanceAndReloadConfig(t *testing.T) {
	cfg1 := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 1},
		Systems: []config.SystemConfig{
			{ID: "keep", Name: "Keep", URL: "https://keep.local"},
			{ID: "drop", Name: "Drop", URL: "https://drop.local"},
		},
	}
	store := NewStore(cfg1)
	store.RecordDNSResult("keep", checker.Result{OK: true, Latency: 20 * time.Millisecond, CheckedAt: time.Now()})

	var lastEv ChangeEvent
	store.SubscribeEvents(func(e ChangeEvent) {
		lastEv = e
	})

	if !store.SetMaintenance("keep", true) {
		t.Fatalf("expected SetMaintenance(keep, true) to succeed")
	}
	store.RecordDNSResult("keep", checker.Result{OK: false, Error: "down", CheckedAt: time.Now()})
	if !lastEv.InMaintenance {
		t.Errorf("expected ChangeEvent.InMaintenance=true during maintenance")
	}

	// ReloadConfig: keep "keep" (preserving history), drop "drop", add "added"
	cfg2 := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 1},
		Systems: []config.SystemConfig{
			{ID: "keep", Name: "Keep Renamed", URL: "https://keep.local", Maintenance: false},
			{ID: "added", Name: "Added", IP: "10.0.0.99"},
		},
	}
	store.ReloadConfig(cfg2)

	if _, ok := store.GetSystem("drop"); ok {
		t.Errorf("expected 'drop' to be removed after ReloadConfig")
	}
	if _, ok := store.GetSystem("added"); !ok {
		t.Errorf("expected 'added' to exist after ReloadConfig")
	}
	keepSnap, ok := store.GetSystem("keep")
	if !ok {
		t.Fatalf("expected 'keep' to exist after ReloadConfig")
	}
	if keepSnap.Name != "Keep Renamed" || keepSnap.InMaintenance {
		t.Errorf("expected updated name and maintenance=false, got %q maint=%v", keepSnap.Name, keepSnap.InMaintenance)
	}
	if !keepSnap.DNS.HasHistory {
		t.Errorf("expected ReloadConfig to preserve in-memory history for retained system")
	}
}

func TestStore_GetSummaryCounts(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:        10 * time.Second,
			FailThreshold:   2,
			DegradedLatency: 500 * time.Millisecond,
		},
		Systems: []config.SystemConfig{
			{ID: "up-sys", Name: "Up", URL: "https://up.local"},
			{ID: "deg-sys", Name: "Deg", URL: "https://deg.local"},
			{ID: "down-sys", Name: "Down", IP: "10.0.0.2"},
			{ID: "unk-sys", Name: "Unk", URL: "https://unk.local"},
		},
	}

	store := NewStore(cfg)
	now := time.Now()
	store.RecordDNSResult("up-sys", checker.Result{OK: true, Latency: 20 * time.Millisecond, CheckedAt: now})
	store.RecordDNSResult("deg-sys", checker.Result{OK: true, Latency: 800 * time.Millisecond, CheckedAt: now})
	store.RecordIPResult("down-sys", checker.Result{OK: false, Error: "timeout", CheckedAt: now})
	store.RecordIPResult("down-sys", checker.Result{OK: false, Error: "timeout", CheckedAt: now})

	summary := store.GetSummaryCounts()
	stats := store.GetStats()

	if summary != stats {
		t.Fatalf("GetSummaryCounts() = %+v, want %+v", summary, stats)
	}
	if summary.Total != 4 || summary.UpCount != 1 || summary.DegradedCount != 1 || summary.DownCount != 1 || summary.UnknownCount != 1 {
		t.Fatalf("unexpected counts: %+v", summary)
	}
	if summary.WorstStatus != StatusDown {
		t.Fatalf("expected WorstStatus DOWN, got %s", summary.WorstStatus)
	}
}

func TestStore_DiagnosisCustomTLSWarnDays(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:      10 * time.Second,
			FailThreshold: 2,
			TLSWarnDays:   30,
		},
		Systems: []config.SystemConfig{
			{ID: "tls-sys", Name: "TLS System", URL: "https://tls.local"},
		},
	}

	store := NewStore(cfg)
	store.RecordDNSResult("tls-sys", checker.Result{
		OK:          true,
		StatusCode:  200,
		Latency:     20 * time.Millisecond,
		HasTLS:      true,
		TLSDaysLeft: 22,
		CheckedAt:   time.Now(),
	})

	snap, ok := store.GetSystem("tls-sys")
	if !ok {
		t.Fatalf("tls-sys not found")
	}
	if snap.WorstStatus != StatusDegraded {
		t.Fatalf("expected StatusDegraded for 22d < 30d warn threshold, got %s", snap.WorstStatus)
	}
	if snap.Diagnosis != "Certificado TLS expira em 22d" {
		t.Errorf("expected TLS expiry diagnosis, got %q", snap.Diagnosis)
	}
}

func TestStore_RingBufferCoversFullHourAt10s(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:      10 * time.Second,
			FailThreshold: 2,
		},
		Systems: []config.SystemConfig{
			{ID: "fast", Name: "Fast System", URL: "https://fast.local"},
		},
	}

	store := NewStore(cfg)
	now := time.Now()
	// First check 59 minutes ago fails, followed by 359 OK checks every 10s
	store.RecordDNSResult("fast", checker.Result{
		OK:        false,
		Error:     "fail at start of hour",
		CheckedAt: now.Add(-59 * time.Minute),
	})
	for i := 1; i < 360; i++ {
		store.RecordDNSResult("fast", checker.Result{
			OK:        true,
			Latency:   10 * time.Millisecond,
			CheckedAt: now.Add(-59*time.Minute + time.Duration(i)*10*time.Second),
		})
	}

	snap, _ := store.GetSystem("fast")
	if snap.Uptime1h >= 100.0 || snap.Uptime1h < 99.5 {
		t.Errorf("expected Uptime1h around 99.72%% (359/360 retained in ring buffer), got %.4f%%", snap.Uptime1h)
	}
}

func TestStore_TimedMaintenanceReappliedAfterReload(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{Interval: 10 * time.Second, FailThreshold: 1},
		Systems:  []config.SystemConfig{{ID: "sys", Name: "Sys", URL: "https://sys.local"}},
	}
	store := NewStore(cfg)
	until := time.Now().Add(time.Hour)

	store.ReloadConfig(cfg)
	if !store.SetMaintenanceUntil("sys", true, until) {
		t.Fatalf("expected SetMaintenanceUntil to succeed")
	}
	snap, _ := store.GetSystem("sys")
	if !snap.InMaintenance {
		t.Fatalf("expected system in maintenance before expiration")
	}

	store.mu.RLock()
	gotUntil := store.systems["sys"].maintenanceUntil
	store.mu.RUnlock()
	if !gotUntil.Equal(until) {
		t.Errorf("expected maintenanceUntil %v, got %v", until, gotUntil)
	}

	store.SetMaintenanceUntil("sys", true, time.Now().Add(-time.Second))
	snap, _ = store.GetSystem("sys")
	if snap.InMaintenance {
		t.Errorf("expected expired timed maintenance to be inactive")
	}
}
