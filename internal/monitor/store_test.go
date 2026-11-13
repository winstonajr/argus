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
		name        string
		dnsResult   checker.Result
		ipResult    checker.Result
		wantDiag    string
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
