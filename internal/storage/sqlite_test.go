package storage

import (
	"context"
	"database/sql"
	"os"
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
	if !overrides["sys-1"].Enabled {
		t.Errorf("expected sys-1 maintenance override to be true, got %+v", overrides)
	}

	if err := db.SetMaintenanceOverride(ctx, "sys-1", false); err != nil {
		t.Fatalf("SetMaintenanceOverride(false) failed: %v", err)
	}
	overrides, err = db.GetMaintenanceOverrides(ctx)
	if err != nil {
		t.Fatalf("GetMaintenanceOverrides failed: %v", err)
	}
	if val, ok := overrides["sys-1"]; !ok || val.Enabled {
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

func TestStorage_MigrationV1ToV2AndHourlyRollups(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_v1.db")

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	_, err = rawDB.Exec(`
CREATE TABLE checks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  system_id   TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  ok          INTEGER NOT NULL,
  status_code INTEGER,
  latency_ms  INTEGER,
  loss_pct    REAL,
  error_cat   TEXT,
  error_msg   TEXT,
  checked_at  DATETIME NOT NULL
);
CREATE TABLE events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  system_id   TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  from_status TEXT    NOT NULL,
  to_status   TEXT    NOT NULL,
  diagnosis   TEXT,
  detail      TEXT,
  occurred_at DATETIME NOT NULL
);
CREATE TABLE maintenance_overrides (
  system_id  TEXT PRIMARY KEY,
  enabled    INTEGER NOT NULL,
  updated_at DATETIME NOT NULL
);
`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyCheckTime := now.Add(-15 * 24 * time.Hour).String()
	_, err = rawDB.Exec(
		`INSERT INTO checks (system_id, kind, ok, status_code, latency_ms, loss_pct, checked_at) VALUES ('erp', 'dns', 1, 200, 40, 0, ?), ('erp', 'dns', 0, 502, 120, 0, ?)`,
		legacyCheckTime, legacyCheckTime,
	)
	if err != nil {
		t.Fatalf("insert legacy checks: %v", err)
	}
	_ = rawDB.Close()

	// Re-open with storage.Open -> should create .bak, migrate to v2, and backfill checks_hourly
	db, err := Open(dbPath, 7*24*time.Hour, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Open migrated db: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath + ".bak"); err != nil {
		t.Errorf("expected backup file %s.bak to be created: %v", dbPath, err)
	}

	// Purge raw checks older than 7d; 15d-old data must remain in checks_hourly for 30d queries!
	if err := db.PurgeOldRecords(); err != nil {
		t.Fatalf("PurgeOldRecords: %v", err)
	}

	ctx := context.Background()
	stats, err := db.GetSystemUptimeStats(ctx, "erp", now.Add(-30*24*time.Hour), now)
	if err != nil {
		t.Fatalf("GetSystemUptimeStats(30d): %v", err)
	}
	if stats.DNSUptime != 50.0 {
		t.Errorf("expected 30d DNSUptime from hourly rollup to be 50.0%% after raw check purge, got %.2f%%", stats.DNSUptime)
	}

	buckets, err := db.GetTimelineBucketsWithLatency(ctx, "erp", now.Add(-30*24*time.Hour), now, 90, 2000)
	if err != nil {
		t.Fatalf("GetTimelineBucketsWithLatency(30d): %v", err)
	}
	hasDataBucket := false
	for _, b := range buckets {
		if b.HasData && b.DNSStatus == "DOWN" {
			hasDataBucket = true
			break
		}
	}
	if !hasDataBucket {
		t.Errorf("expected at least one 30d timeline bucket with HasData=true and DNSStatus=DOWN from rollup")
	}
}

func TestStorage_TimedMaintenanceSurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maint.db")
	ctx := context.Background()

	db, err := Open(dbPath, 24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	until := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	if err := db.SetMaintenanceOverrideUntil(ctx, "timed", true, until); err != nil {
		t.Fatalf("SetMaintenanceOverrideUntil(timed): %v", err)
	}
	if err := db.SetMaintenanceOverrideUntil(ctx, "expired", true, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("SetMaintenanceOverrideUntil(expired): %v", err)
	}
	if err := db.SetMaintenanceOverride(ctx, "forever", true); err != nil {
		t.Fatalf("SetMaintenanceOverride(forever): %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db, err = Open(dbPath, 24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()

	overrides, err := db.GetMaintenanceOverrides(ctx)
	if err != nil {
		t.Fatalf("GetMaintenanceOverrides: %v", err)
	}
	if ov := overrides["timed"]; !ov.Enabled || !ov.Until.Equal(until) {
		t.Errorf("timed override: want enabled until %v, got %+v", until, ov)
	}
	if ov := overrides["expired"]; ov.Enabled || !ov.Until.IsZero() {
		t.Errorf("expired override: want disabled, got %+v", ov)
	}
	if ov := overrides["forever"]; !ov.Enabled || !ov.Until.IsZero() {
		t.Errorf("forever override: want enabled without expiration, got %+v", ov)
	}
}

func TestSQLite_V3MigrationBreakdownAndNotes(t *testing.T) {
	db, err := Open(":memory:", 24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var ver int
	if err := db.db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if ver != 3 {
		t.Fatalf("expected user_version=3, got %d", ver)
	}

	now := time.Now().UTC()
	err = db.writeBatch([]writeItem{
		{
			itemType: itemCheck,
			sysID:    "erp",
			kind:     "dns",
			check: &checker.Result{
				OK:             true,
				StatusCode:     200,
				Latency:        100 * time.Millisecond,
				DNSLatency:     10 * time.Millisecond,
				ConnectLatency: 20 * time.Millisecond,
				TLSLatency:     30 * time.Millisecond,
				TTFBLatency:    40 * time.Millisecond,
				CheckedAt:      now,
			},
		},
		{
			itemType: itemEvent,
			sysID:    "erp",
			kind:     "dns",
			event: &monitor.ChangeEvent{
				SystemID:   "erp",
				Kind:       "dns",
				FromStatus: monitor.StatusUP,
				ToStatus:   monitor.StatusDown,
				Diagnosis:  "HTTP 502",
				OccurredAt: now,
			},
		},
	})
	if err != nil {
		t.Fatalf("writeBatch: %v", err)
	}

	ctx := context.Background()
	stats, err := db.GetSystemUptimeStats(ctx, "erp", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetSystemUptimeStats: %v", err)
	}
	if stats.AvgDNSMs != 10 || stats.AvgConnectMs != 20 || stats.AvgTLSMs != 30 || stats.AvgTTFBMs != 40 {
		t.Errorf("unexpected httptrace averages: %+v", stats)
	}

	evs, err := db.ListEvents(ctx, "erp", now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("ListEvents: %v (len=%d)", err, len(evs))
	}
	if err := db.UpdateEventNote(ctx, evs[0].ID, "Reiniciando pod", true); err != nil {
		t.Fatalf("UpdateEventNote: %v", err)
	}
	evs2, err := db.ListEvents(ctx, "erp", now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil || len(evs2) != 1 || evs2[0].Note != "Reiniciando pod" || !evs2[0].PublicNote {
		t.Errorf("expected persisted note & public_note, got %+v", evs2)
	}
}
