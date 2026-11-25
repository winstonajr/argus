package monitor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
)

func TestScheduler_CheckNow(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer httpSrv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	tcpPort := ln.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:        10 * time.Minute,
			FailThreshold:   2,
			HTTPTimeout:     2 * time.Second,
			PingTimeout:     2 * time.Second,
			DegradedLatency: 2 * time.Second,
		},
		Systems: []config.SystemConfig{
			{
				ID:      "app1",
				Name:    "App 1",
				URL:     httpSrv.URL,
				IP:      "127.0.0.1",
				TCPPort: tcpPort,
			},
		},
	}

	store := NewStore(cfg)
	sched := NewScheduler(cfg, store, checker.NewHTTPChecker(), checker.NewPingChecker())
	defer sched.Stop()

	if sched.CheckNow("nonexistent") {
		t.Fatalf("expected CheckNow(nonexistent) to return false")
	}

	if !sched.CheckNow("app1") {
		t.Fatalf("expected CheckNow(app1) to return true")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := store.GetSystem("app1")
		if ok && snap.DNS.Status == StatusUP && snap.IPCheck.Status == StatusUP {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	snap, _ := store.GetSystem("app1")
	t.Fatalf("expected both DNS and IP to become UP after CheckNow, got DNS=%s IP=%s", snap.DNS.Status, snap.IPCheck.Status)
}

func TestScheduler_OfflinePausePreservesHistory(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:        10 * time.Minute,
			FailThreshold:   1,
			HTTPTimeout:     200 * time.Millisecond,
			PingTimeout:     200 * time.Millisecond,
			DegradedLatency: 200 * time.Millisecond,
		},
		Systems: []config.SystemConfig{
			{
				ID:      "ext",
				Name:    "External",
				URL:     "http://127.0.0.1:1", // guaranteed connection refused
				IP:      "127.0.0.1",
				TCPPort: 1,
			},
		},
	}

	store := NewStore(cfg)
	store.RecordDNSResult("ext", checker.Result{OK: true, Latency: 10 * time.Millisecond, CheckedAt: time.Now()})
	store.RecordIPResult("ext", checker.Result{OK: true, Latency: 5 * time.Millisecond, CheckedAt: time.Now()})

	var online atomic.Bool
	online.Store(false)

	sched := NewScheduler(cfg, store, checker.NewHTTPChecker(), checker.NewPingChecker())
	sched.SetConnectivityProbe(func(ctx context.Context) bool {
		return online.Load()
	})

	ctx := context.Background()
	sched.executeDNS(ctx, cfg.Systems[0])
	sched.executeIP(ctx, cfg.Systems[0])

	if !sched.IsOffline() {
		t.Fatalf("expected scheduler to enter offline mode when probe returns false")
	}
	if sched.CheckNow("ext") {
		t.Fatalf("expected CheckNow to return false while offline")
	}

	snap, _ := store.GetSystem("ext")
	if snap.WorstStatus != StatusUP {
		t.Fatalf("expected system status to stay UP while offline, got %s", snap.WorstStatus)
	}

	online.Store(true)
	if sched.probeAndUpdate(ctx, false) {
		t.Fatalf("expected scheduler to recover online when probe returns true")
	}
	if sched.IsOffline() {
		t.Fatalf("expected IsOffline() to be false after recovery")
	}
}

func TestStore_TimeoutsStayDegradedNotDown(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			Interval:        30 * time.Second,
			FailThreshold:   2,
			DegradedLatency: 2 * time.Second,
		},
		Systems: []config.SystemConfig{
			{ID: "slow", Name: "Slow App", URL: "https://slow.example.com"},
		},
	}

	store := NewStore(cfg)
	now := time.Now()
	for i := 0; i < 5; i++ {
		store.RecordDNSResult("slow", checker.Result{
			OK:        false,
			ErrorCat:  checker.CatTimeout,
			Error:     "request timeout",
			CheckedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	snap, ok := store.GetSystem("slow")
	if !ok {
		t.Fatalf("expected system slow in store")
	}
	if snap.WorstStatus != StatusDegraded {
		t.Fatalf("expected repeated timeouts to remain DEGRADED, got %s", snap.WorstStatus)
	}
}
