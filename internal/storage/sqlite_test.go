package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"argus/internal/checker"
	"argus/internal/monitor"
)

func TestStorage_SQLiteFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_argus.db")

	db, err := Open(dbPath, 7*24*time.Hour, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("failed to ping db: %v", err)
	}

	now := time.Now().UTC()

	// 1. Save checks
	db.AsyncSaveCheck("sys-1", "dns", checker.Result{
		OK:         true,
		StatusCode: 200,
		Latency:    45 * time.Millisecond,
		CheckedAt:  now.Add(-10 * time.Minute),
	})
	db.AsyncSaveCheck("sys-1", "dns", checker.Result{
		OK:         false,
		StatusCode: 502,
		Latency:    100 * time.Millisecond,
		ErrorCat:   checker.CatProxy,
		Error:      "Cloudflare 502 Bad Gateway",
		CheckedAt:  now.Add(-5 * time.Minute),
	})
	db.AsyncSaveCheck("sys-1", "ip", checker.Result{
		OK:        true,
		Latency:   3 * time.Millisecond,
		CheckedAt: now.Add(-5 * time.Minute),
	})

	// 2. Save events
	db.AsyncSaveEvent(monitor.ChangeEvent{
		SystemID:   "sys-1",
		SystemName: "System One",
		Kind:       "dns",
		FromStatus: monitor.StatusUP,
		ToStatus:   monitor.StatusDown,
		Diagnosis:  "Aplicação fora (borda Cloudflare)",
		Detail:     "Cloudflare 502; VM/IP operacional",
		OccurredAt: now.Add(-5 * time.Minute),
	})
	db.AsyncSaveEvent(monitor.ChangeEvent{
		SystemID:   "sys-1",
		SystemName: "System One",
		Kind:       "dns",
		FromStatus: monitor.StatusDown,
		ToStatus:   monitor.StatusUP,
		Diagnosis:  "Operacional",
		OccurredAt: now.Add(-1 * time.Minute),
	})

	// Wait 1.2s for writer ticker flush
	time.Sleep(1200 * time.Millisecond)

	ctx := context.Background()

	// Query events
	events, err := db.ListEvents(ctx, "sys-1", now.Add(-1*time.Hour), now.Add(1*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ToStatus != "UP" || events[1].ToStatus != "DOWN" {
		t.Errorf("expected descending event order")
	}
	if events[0].Duration <= 0 {
		t.Errorf("expected positive duration computed for recovery event, got %v", events[0].Duration)
	}

	// Query failed checks
	failed, err := db.ListFailedChecks(ctx, "sys-1", now.Add(-1*time.Hour), now.Add(1*time.Hour), 10, 0)
	if err != nil {
		t.Fatalf("ListFailedChecks failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed check, got %d", len(failed))
	}
	if failed[0].ErrorCat != checker.CatProxy {
		t.Errorf("expected CatProxy, got %q", failed[0].ErrorCat)
	}

	// Timeline buckets
	buckets, err := db.GetTimelineBuckets(ctx, "sys-1", now.Add(-30*time.Minute), now, 30)
	if err != nil {
		t.Fatalf("GetTimelineBuckets failed: %v", err)
	}
	if len(buckets) != 30 {
		t.Errorf("expected 30 buckets, got %d", len(buckets))
	}

	// Uptime stats
	stats, err := db.GetSystemUptimeStats(ctx, "sys-1", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("GetSystemUptimeStats failed: %v", err)
	}
	if stats.IncidentCount != 1 {
		t.Errorf("expected 1 incident, got %d", stats.IncidentCount)
	}
	if stats.DNSUptime != 50.0 { // 1 ok, 1 failed
		t.Errorf("expected 50%% DNS uptime, got %.2f", stats.DNSUptime)
	}
	if stats.IPUptime != 100.0 {
		t.Errorf("expected 100%% IP uptime, got %.2f", stats.IPUptime)
	}

	// Recent checks (oldest to newest)
	recent, err := db.GetRecentChecks(ctx, "sys-1", "dns", 10)
	if err != nil {
		t.Fatalf("GetRecentChecks failed: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent checks, got %d", len(recent))
	}
	if !recent[0].OK || recent[1].OK {
		t.Errorf("expected chronological order (OK first, then fail), got %+v", recent)
	}

	latestEv, err := db.GetLatestEventTime(ctx, "sys-1", "dns")
	if err != nil {
		t.Fatalf("GetLatestEventTime failed: %v", err)
	}
	if latestEv.IsZero() {
		t.Errorf("expected non-zero latest event time")
	}

	if err := db.SetMaintenanceOverride(ctx, "sys-1", true); err != nil {
		t.Fatalf("SetMaintenanceOverride(true) failed: %v", err)
	}
	overrides, err := db.GetMaintenanceOverrides(ctx)
	if err != nil {
		t.Fatalf("GetMaintenanceOverrides failed: %v", err)
	}
	if !overrides["sys-1"] {
		t.Errorf("expected sys-1 maintenance override to be true, got %+v", overrides)
	}

	if err := db.SetMaintenanceOverride(ctx, "sys-1", false); err != nil {
		t.Fatalf("SetMaintenanceOverride(false) failed: %v", err)
	}
	overrides, err = db.GetMaintenanceOverrides(ctx)
	if err != nil {
		t.Fatalf("GetMaintenanceOverrides failed: %v", err)
	}
	if val, ok := overrides["sys-1"]; !ok || val {
		t.Errorf("expected sys-1 maintenance override to be false, got %+v", overrides)
	}
}

func TestStorage_ListEventsInterleavedKindsMTTR(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_interleaved.db")

	db, err := Open(dbPath, 7*24*time.Hour, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	base := time.Now().UTC().Truncate(time.Second)
	rawEvents := []monitor.ChangeEvent{
		{SystemID: "s1", Kind: "dns", FromStatus: monitor.StatusUP, ToStatus: monitor.StatusDown, OccurredAt: base.Add(-10 * time.Minute)},
		{SystemID: "s1", Kind: "ip", FromStatus: monitor.StatusUP, ToStatus: monitor.StatusDown, OccurredAt: base.Add(-8 * time.Minute)},
		{SystemID: "s1", Kind: "dns", FromStatus: monitor.StatusDown, ToStatus: monitor.StatusUP, OccurredAt: base.Add(-5 * time.Minute)},
		{SystemID: "s1", Kind: "ip", FromStatus: monitor.StatusDown, ToStatus: monitor.StatusUP, OccurredAt: base.Add(-2 * time.Minute)},
	}
	for _, ev := range rawEvents {
		db.AsyncSaveEvent(ev)
	}
	time.Sleep(1200 * time.Millisecond)

	ctx := context.Background()
	events, err := db.ListEvents(ctx, "s1", base.Add(-15*time.Minute), base, 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	var dnsDur, ipDur time.Duration
	for _, ev := range events {
		if ev.ToStatus == "UP" && ev.Kind == "dns" {
			dnsDur = ev.Duration
		}
		if ev.ToStatus == "UP" && ev.Kind == "ip" {
			ipDur = ev.Duration
		}
	}
	if dnsDur != 5*time.Minute {
		t.Errorf("expected DNS recovery duration 5m, got %v", dnsDur)
	}
	if ipDur != 6*time.Minute {
		t.Errorf("expected IP recovery duration 6m, got %v", ipDur)
	}
}
