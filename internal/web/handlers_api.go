package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"argus/internal/monitor"
	"argus/internal/notify"
	"argus/internal/storage"
)

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

func (s *Server) handleAPISystems(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(systems)
}

func (s *Server) handleAPICards(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	systems := s.store.GetAllSystems()
	statsHTML, _ := s.renderStatsHTML(t)

	type cardItem struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		HTML   string `json:"html"`
	}
	cards := make([]cardItem, 0, len(systems))
	for _, sys := range systems {
		var buf bytes.Buffer
		vm := toCardViewModelWithLang(sys, t)
		if err := s.cardTmpl.Execute(&buf, vm); err == nil {
			cards = append(cards, cardItem{
				ID:     sys.ID,
				Status: string(sys.WorstStatus),
				HTML:   buf.String(),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"stats_html": statsHTML,
		"cards":      cards,
	})
}

func (s *Server) handleAPIFailedChecks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.GetSystem(id); !ok {
		http.Error(w, `{"error":"system not found"}`, http.StatusNotFound)
		return
	}

	t := s.messagesForRequest(w, r)
	_, _, duration, _ := parsePeriod(r.URL.Query().Get("period"), t)
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	to := time.Now().UTC()
	from := to.Add(-duration)

	var items []failedCheckViewModel
	if s.storage != nil {
		failed, err := s.storage.ListFailedChecks(r.Context(), id, from, to, 50, offset)
		if err == nil {
			items = make([]failedCheckViewModel, len(failed))
			for i, f := range failed {
				items[i] = failedCheckViewModel{
					Kind:               strings.ToUpper(f.Kind),
					StatusCode:         f.StatusCode,
					LatencyMs:          f.LatencyMs,
					ErrorCat:           f.ErrorCat,
					ErrorMsg:           f.ErrorMsg,
					CheckedAtFormatted: f.CheckedAt.Local().Format("02/01 15:04:05"),
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":    items,
		"has_more": len(items) >= 50,
	})
}

func (s *Server) handleToggleMaintenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, `{"error":"system not found"}`, http.StatusNotFound)
		return
	}

	next := !snap.InMaintenance
	if q := r.URL.Query().Get("enabled"); q != "" {
		next = q == "true" || q == "1"
	}

	var until time.Time
	if next {
		if durStr := r.URL.Query().Get("duration"); durStr != "" {
			if d, err := time.ParseDuration(durStr); err == nil && d > 0 {
				until = time.Now().Add(d)
			}
		}
	}

	s.store.SetMaintenanceUntil(id, next, until)
	if s.storage != nil {
		if err := s.storage.SetMaintenanceOverrideUntil(r.Context(), id, next, until); err != nil {
			log.Printf("[WARN] persist maintenance override for %s: %v", id, err)
		}
	}
	s.broadcastSystemUpdate(id)

	resp := map[string]any{
		"system_id":      id,
		"in_maintenance": next,
	}
	if !until.IsZero() {
		resp["until"] = until.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleTestAlert(w http.ResponseWriter, r *http.Request) {
	s.notifierMu.RLock()
	n := s.notifier
	s.notifierMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if n == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "notification service unavailable"})
		return
	}

	const alertTestCooldown = 3 * time.Second
	now := time.Now()
	s.manualCheckMu.Lock()
	if !s.lastAlertTest.IsZero() && now.Sub(s.lastAlertTest) < alertTestCooldown {
		s.manualCheckMu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "rate limit exceeded",
		})
		return
	}
	s.lastAlertTest = now
	s.manualCheckMu.Unlock()

	sent, err := n.SendTest(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, notify.ErrNoChannelsConfigured) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       false,
			"channels": sent,
			"error":    err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"channels": sent,
	})
}

func (s *Server) handleTriggerCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.GetSystem(id); !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "system not found"})
		return
	}

	s.triggerMu.RLock()
	trigger := s.checkTrigger
	s.triggerMu.RUnlock()
	if trigger == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "check trigger unavailable"})
		return
	}

	const cooldown = 3 * time.Second
	now := time.Now()
	s.manualCheckMu.Lock()
	if last, exists := s.lastManualCheck[id]; exists && now.Sub(last) < cooldown {
		remaining := cooldown - now.Sub(last)
		retryAfter := int((remaining + time.Second - 1) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		s.manualCheckMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":               "rate limit exceeded",
			"retry_after_seconds": retryAfter,
		})
		return
	}
	s.lastManualCheck[id] = now
	s.manualCheckMu.Unlock()

	if !trigger(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "system not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"system_id": id,
		"status":    "scheduled",
	})
}

func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	var events []storage.StoredEvent
	if s.storage != nil {
		var err error
		events, err = s.storage.ListAllRecentEvents(r.Context(), 100)
		if err != nil {
			http.Error(w, `{"error":"failed to load events"}`, http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	systems := s.store.GetAllSystems()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var b strings.Builder
	b.WriteString("# HELP argus_system_up System health state (1=UP, 0.5=DEGRADED, 0=DOWN)\n")
	b.WriteString("# TYPE argus_system_up gauge\n")
	for _, sys := range systems {
		val := 0.0
		switch sys.WorstStatus {
		case monitor.StatusUP:
			val = 1.0
		case monitor.StatusDegraded:
			val = 0.5
		}
		fmt.Fprintf(&b, "argus_system_up{id=%q,name=%q,group=%q} %.1f\n", sys.ID, sys.Name, sys.Group, val)
	}

	b.WriteString("# HELP argus_check_up Individual check state (1=UP, 0.5=DEGRADED, 0=DOWN)\n")
	b.WriteString("# TYPE argus_check_up gauge\n")
	for _, sys := range systems {
		if sys.DNS.Configured && sys.DNS.HasHistory {
			fmt.Fprintf(&b, "argus_check_up{id=%q,kind=\"dns\"} %.1f\n", sys.ID, statusMetricValue(sys.DNS.Status))
		}
		if sys.IPCheck.Configured && sys.IPCheck.HasHistory {
			fmt.Fprintf(&b, "argus_check_up{id=%q,kind=\"ip\"} %.1f\n", sys.ID, statusMetricValue(sys.IPCheck.Status))
		}
	}

	b.WriteString("# HELP argus_uptime_1h_percent Rolling 1-hour uptime percentage (0..100)\n")
	b.WriteString("# TYPE argus_uptime_1h_percent gauge\n")
	for _, sys := range systems {
		fmt.Fprintf(&b, "argus_uptime_1h_percent{id=%q} %.2f\n", sys.ID, sys.Uptime1h)
	}

	b.WriteString("# HELP argus_check_latency_seconds Last observed check latency in seconds\n")
	b.WriteString("# TYPE argus_check_latency_seconds gauge\n")
	for _, sys := range systems {
		if sys.DNS.Configured && sys.DNS.HasHistory {
			fmt.Fprintf(&b, "argus_check_latency_seconds{id=%q,kind=\"dns\"} %.6f\n", sys.ID, sys.DNS.LastResult.Latency.Seconds())
		}
		if sys.IPCheck.Configured && sys.IPCheck.HasHistory {
			fmt.Fprintf(&b, "argus_check_latency_seconds{id=%q,kind=\"ip\"} %.6f\n", sys.ID, sys.IPCheck.LastResult.Latency.Seconds())
		}
	}

	b.WriteString("# HELP argus_check_packet_loss_percent Last observed ICMP packet loss percentage (0..100)\n")
	b.WriteString("# TYPE argus_check_packet_loss_percent gauge\n")
	for _, sys := range systems {
		if sys.IPCheck.Configured && sys.IPCheck.HasHistory {
			fmt.Fprintf(&b, "argus_check_packet_loss_percent{id=%q} %.2f\n", sys.ID, sys.IPCheck.LastResult.Loss)
		}
	}

	b.WriteString("# HELP argus_check_packet_loss_ratio Last observed ICMP packet loss ratio (0..1)\n")
	b.WriteString("# TYPE argus_check_packet_loss_ratio gauge\n")
	for _, sys := range systems {
		if sys.IPCheck.Configured && sys.IPCheck.HasHistory {
			fmt.Fprintf(&b, "argus_check_packet_loss_ratio{id=%q} %.4f\n", sys.ID, sys.IPCheck.LastResult.Loss/100.0)
		}
	}

	b.WriteString("# HELP argus_tls_cert_expiry_days Days remaining until TLS certificate expiration\n")
	b.WriteString("# TYPE argus_tls_cert_expiry_days gauge\n")
	for _, sys := range systems {
		if sys.DNS.Configured && sys.DNS.LastResult.HasTLS {
			fmt.Fprintf(&b, "argus_tls_cert_expiry_days{id=%q} %d\n", sys.ID, sys.DNS.LastResult.TLSDaysLeft)
		}
	}

	_, _ = w.Write([]byte(b.String()))
}

func statusMetricValue(st monitor.Status) float64 {
	switch st {
	case monitor.StatusUP:
		return 1.0
	case monitor.StatusDegraded:
		return 0.5
	default:
		return 0.0
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.storage != nil {
		if err := s.storage.Ping(r.Context()); err != nil {
			log.Printf("[WARN] healthz db ping failed: %v", err)
			http.Error(w, "db unhealthy", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
