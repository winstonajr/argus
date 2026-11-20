package notify

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"argus/internal/config"
	"argus/internal/monitor"
)

func TestNotifier_WebhookCooldownAndRecovery(t *testing.T) {
	var hits atomic.Int32
	var lastPayload map[string]any
	done := make(chan struct{}, 10)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &lastPayload)
		w.WriteHeader(http.StatusOK)
		done <- struct{}{}
	}))
	defer ts.Close()

	n := New(config.AlertsConfig{
		Cooldown: 1 * time.Hour,
		Webhook: config.WebhookAlertConfig{
			URL: ts.URL,
			Headers: map[string]string{
				"X-Argus-Secret": "abc",
			},
		},
	})

	if !n.Enabled() {
		t.Fatalf("expected notifier to be enabled")
	}

	now := time.Now()
	// 1. First DOWN event -> should send
	n.HandleEvent(monitor.ChangeEvent{
		SystemID:   "erp",
		SystemName: "ERP",
		Kind:       "dns",
		FromStatus: monitor.StatusUP,
		ToStatus:   monitor.StatusDown,
		Diagnosis:  "Aplicação fora",
		Detail:     "HTTP 502",
		OccurredAt: now.Add(-2 * time.Minute),
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for first webhook")
	}

	// 2. Second DOWN event within cooldown -> should NOT send
	n.HandleEvent(monitor.ChangeEvent{
		SystemID:   "erp",
		SystemName: "ERP",
		Kind:       "dns",
		FromStatus: monitor.StatusUP,
		ToStatus:   monitor.StatusDown,
		OccurredAt: now.Add(-1 * time.Minute),
	})
	time.Sleep(50 * time.Millisecond)
	if hits.Load() != 1 {
		t.Fatalf("expected cooldown to suppress second DOWN alert, got %d hits", hits.Load())
	}

	// 3. Recovery event -> should send with mttr_seconds > 0
	n.HandleEvent(monitor.ChangeEvent{
		SystemID:   "erp",
		SystemName: "ERP",
		Kind:       "dns",
		FromStatus: monitor.StatusDown,
		ToStatus:   monitor.StatusUP,
		Diagnosis:  "Operacional",
		OccurredAt: now,
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for recovery webhook")
	}

	if hits.Load() != 2 {
		t.Fatalf("expected 2 webhook calls total, got %d", hits.Load())
	}
	if mttr, ok := lastPayload["mttr_seconds"].(float64); !ok || mttr < 100 {
		t.Errorf("expected mttr_seconds >= 100, got %v", lastPayload["mttr_seconds"])
	}
}

func TestNotifier_SlackDiscordTelegramAndMaintenance(t *testing.T) {
	var mu sync.Mutex
	received := make(map[string]map[string]any)
	done := make(chan string, 10)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		received[r.URL.Path] = m
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		done <- r.URL.Path
	}))
	defer ts.Close()

	n := New(config.AlertsConfig{
		Slack:    config.SlackAlertConfig{WebhookURL: ts.URL + "/slack"},
		Discord:  config.DiscordAlertConfig{WebhookURL: ts.URL + "/discord"},
		Telegram: config.TelegramAlertConfig{BotToken: "123:ABC", ChatID: "-100999"},
	})
	n.telegramBaseURL = ts.URL

	// Maintenance event must be suppressed
	n.HandleEvent(monitor.ChangeEvent{
		SystemID:      "erp",
		SystemName:    "ERP",
		Kind:          "dns",
		FromStatus:    monitor.StatusUP,
		ToStatus:      monitor.StatusDown,
		InMaintenance: true,
	})
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(received) != 0 {
		t.Fatalf("expected maintenance event to be suppressed, got %v", received)
	}
	mu.Unlock()

	// Real DOWN event must hit Slack, Discord, Telegram
	n.HandleEvent(monitor.ChangeEvent{
		SystemID:   "erp",
		SystemName: "ERP",
		Kind:       "dns",
		FromStatus: monitor.StatusUP,
		ToStatus:   monitor.StatusDown,
		Diagnosis:  "Borda Cloudflare 522",
		OccurredAt: time.Now(),
	})

	gotPaths := make(map[string]bool)
	for i := 0; i < 3; i++ {
		select {
		case p := <-done:
			gotPaths[p] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for all 3 notification channels; got %v", gotPaths)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(received["/slack"]["text"].(string), "ERP") {
		t.Errorf("unexpected slack payload: %v", received["/slack"])
	}
	if !strings.Contains(received["/discord"]["content"].(string), "ERP") {
		t.Errorf("unexpected discord payload: %v", received["/discord"])
	}
	tgPayload := received["/bot123:ABC/sendMessage"]
	if tgPayload == nil || tgPayload["chat_id"] != "-100999" {
		t.Errorf("unexpected telegram payload: %v", tgPayload)
	}
}

func TestNotifier_SendEmailRespectsContextTimeout(t *testing.T) {
	// Start a TCP listener that accepts connections but never sends SMTP greeting
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err == nil {
			time.Sleep(2 * time.Second)
			_ = conn.Close()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	n := New(config.AlertsConfig{
		Email: config.EmailAlertConfig{
			SMTPHost: "127.0.0.1",
			SMTPPort: addr.Port,
			From:     "argus@local",
			To:       []string{"ops@local"},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = n.sendEmail(ctx, n.cfg.Email, "Teste", "Corpo")
	if err == nil {
		t.Fatalf("expected sendEmail to fail on unresponsive SMTP server timeout")
	}
	if time.Since(start) > time.Second {
		t.Errorf("sendEmail took too long (%v), did not respect context timeout", time.Since(start))
	}
}

func TestNotifier_SendTest(t *testing.T) {
	emptyNotifier := New(config.AlertsConfig{})
	if _, err := emptyNotifier.SendTest(context.Background()); err != ErrNoChannelsConfigured {
		t.Fatalf("expected ErrNoChannelsConfigured, got %v", err)
	}

	var called int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(config.AlertsConfig{
		Webhook: config.WebhookAlertConfig{URL: srv.URL},
	})
	sent, err := n.SendTest(context.Background())
	if err != nil {
		t.Fatalf("SendTest failed: %v", err)
	}
	if sent != 1 || called != 1 {
		t.Errorf("expected sent=1 and called=1, got sent=%d called=%d", sent, called)
	}
}

func TestNotifier_SanitizeHeader(t *testing.T) {
	dirty := "Subject\r\nBcc: attacker@evil.com\nInjected: yes"
	clean := sanitizeHeader(dirty)
	if strings.ContainsAny(clean, "\r\n") {
		t.Errorf("sanitizeHeader did not strip CR/LF: %q", clean)
	}
}

func TestNotifier_DegradedCooldownDoesNotSuppressDownAndMaintenanceClearsDownSince(t *testing.T) {
	var mu sync.Mutex
	var events []string
	done := make(chan struct{}, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		events = append(events, r.Method)
		mu.Unlock()
		done <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(config.AlertsConfig{
		Cooldown:         time.Hour,
		NotifyOnDegraded: true,
		Webhook:          config.WebhookAlertConfig{URL: srv.URL},
	})

	n.HandleEvent(monitor.ChangeEvent{
		SystemID:   "api",
		SystemName: "API",
		Kind:       "dns",
		FromStatus: monitor.StatusUP,
		ToStatus:   monitor.StatusDegraded,
		OccurredAt: time.Now(),
	})
	<-done

	n.HandleEvent(monitor.ChangeEvent{
		SystemID:   "api",
		SystemName: "API",
		Kind:       "dns",
		FromStatus: monitor.StatusDegraded,
		ToStatus:   monitor.StatusDown,
		OccurredAt: time.Now(),
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected DOWN alert not to be suppressed by DEGRADED cooldown")
	}

	n.HandleEvent(monitor.ChangeEvent{
		SystemID:      "api",
		SystemName:    "API",
		Kind:          "dns",
		FromStatus:    monitor.StatusDown,
		ToStatus:      monitor.StatusUP,
		InMaintenance: true,
		OccurredAt:    time.Now(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n.Close(ctx)

	n.mu.Lock()
	_, stillDown := n.downSince["api:dns"]
	n.mu.Unlock()
	if stillDown {
		t.Errorf("expected downSince to be cleared when recovering to UP during maintenance")
	}
	if len(events) != 2 {
		t.Errorf("expected 2 webhook calls (DEGRADED and DOWN), got %d", len(events))
	}
}
