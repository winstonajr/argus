package monitor

import (
	"net"
	"net/http"
	"net/http/httptest"
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
