package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"argus/internal/checker"
)

// ListEvents retrieves status change events for a system ordered descending by time.
func (d *DB) ListEvents(ctx context.Context, sysID string, from, to time.Time, limit int) ([]StoredEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
SELECT id, system_id, kind, from_status, to_status, COALESCE(diagnosis, ''), COALESCE(detail, ''), occurred_at
FROM events
WHERE system_id = ? AND occurred_at >= ? AND occurred_at <= ?
ORDER BY occurred_at DESC
LIMIT ?
`
	rows, err := d.db.QueryContext(ctx, query, sysID, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []StoredEvent
	for rows.Next() {
		var ev StoredEvent
		var occStr string
		err := rows.Scan(
			&ev.ID,
			&ev.SystemID,
			&ev.Kind,
			&ev.FromStatus,
			&ev.ToStatus,
			&ev.Diagnosis,
			&ev.Detail,
			&occStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.OccurredAt, _ = parseTime(occStr)
		events = append(events, ev)
	}

	pendingUp := make(map[string]int)
	for i := range events {
		switch events[i].ToStatus {
		case "UP":
			pendingUp[events[i].Kind] = i
		case "DOWN":
			if j, ok := pendingUp[events[i].Kind]; ok {
				if d := events[j].OccurredAt.Sub(events[i].OccurredAt); d > 0 {
					events[j].Duration = d
				}
				delete(pendingUp, events[i].Kind)
			}
		}
	}

	return events, rows.Err()
}

// ListFailedChecks returns checks where ok = 0 with pagination.
func (d *DB) ListFailedChecks(ctx context.Context, sysID string, from, to time.Time, limit, offset int) ([]StoredCheck, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
SELECT id, system_id, kind, ok, COALESCE(status_code, 0), COALESCE(latency_ms, 0), COALESCE(loss_pct, 0),
       COALESCE(error_cat, ''), COALESCE(error_msg, ''), checked_at
FROM checks
WHERE system_id = ? AND ok = 0 AND checked_at >= ? AND checked_at <= ?
ORDER BY checked_at DESC
LIMIT ? OFFSET ?
`
	rows, err := d.db.QueryContext(ctx, query, sysID, from.UTC(), to.UTC(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query failed checks: %w", err)
	}
	defer rows.Close()

	var checks []StoredCheck
	for rows.Next() {
		var c StoredCheck
		var okInt int
		var checkStr string
		err := rows.Scan(
			&c.ID,
			&c.SystemID,
			&c.Kind,
			&okInt,
			&c.StatusCode,
			&c.LatencyMs,
			&c.LossPct,
			&c.ErrorCat,
			&c.ErrorMsg,
			&checkStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan failed check: %w", err)
		}
		c.OK = okInt == 1
		c.CheckedAt, _ = parseTime(checkStr)
		checks = append(checks, c)
	}

	return checks, rows.Err()
}

// GetTimelineBuckets computes slice of time buckets and their worst status.
func (d *DB) GetTimelineBuckets(ctx context.Context, sysID string, from, to time.Time, bucketCount int) ([]TimelineBucket, error) {
	return d.GetTimelineBucketsWithLatency(ctx, sysID, from, to, bucketCount, 2000)
}

// GetTimelineBucketsWithLatency computes time buckets marking DEGRADED when latency exceeds degradedLatencyMs.
func (d *DB) GetTimelineBucketsWithLatency(ctx context.Context, sysID string, from, to time.Time, bucketCount int, degradedLatencyMs int64) ([]TimelineBucket, error) {
	if bucketCount <= 0 {
		bucketCount = 60
	}

	totalDuration := to.Sub(from)
	if totalDuration <= 0 {
		return nil, fmt.Errorf("invalid time range from %v to %v", from, to)
	}
	bucketDuration := totalDuration / time.Duration(bucketCount)

	buckets := make([]TimelineBucket, bucketCount)
	for i := 0; i < bucketCount; i++ {
		bStart := from.Add(time.Duration(i) * bucketDuration)
		bEnd := bStart.Add(bucketDuration)
		buckets[i] = TimelineBucket{
			StartTime: bStart,
			EndTime:   bEnd,
			DNSStatus: "UP",
			IPStatus:  "UP",
			HasData:   false,
		}
	}

	// Fetch all checks in range
	query := `
SELECT kind, ok, COALESCE(latency_ms, 0), COALESCE(loss_pct, 0), checked_at
FROM checks
WHERE system_id = ? AND checked_at >= ? AND checked_at <= ?
ORDER BY checked_at ASC
`
	rows, err := d.db.QueryContext(ctx, query, sysID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("query checks for timeline: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var kind string
		var okInt int
		var latencyMs int64
		var loss float64
		var checkStr string

		if err := rows.Scan(&kind, &okInt, &latencyMs, &loss, &checkStr); err != nil {
			return nil, fmt.Errorf("scan check for timeline: %w", err)
		}

		checkTime, _ := parseTime(checkStr)
		if checkTime.Before(from) || !checkTime.Before(to) {
			continue
		}

		idx := int(checkTime.Sub(from) / bucketDuration)
		if idx < 0 {
			idx = 0
		} else if idx >= bucketCount {
			idx = bucketCount - 1
		}

		b := &buckets[idx]
		b.HasData = true

		if kind == "dns" {
			if okInt == 0 {
				b.DNSStatus = "DOWN"
			} else if degradedLatencyMs > 0 && latencyMs > degradedLatencyMs && b.DNSStatus != "DOWN" {
				b.DNSStatus = "DEGRADED"
			}
		} else if kind == "ip" {
			if okInt == 0 {
				b.IPStatus = "DOWN"
			} else if (loss > 0 || (degradedLatencyMs > 0 && latencyMs > degradedLatencyMs)) && b.IPStatus != "DOWN" {
				b.IPStatus = "DEGRADED"
			}
		}
	}

	return buckets, rows.Err()
}

// GetSystemUptimeStats calculates DNS and IP uptime percentage and incident counts in range.
func (d *DB) GetSystemUptimeStats(ctx context.Context, sysID string, from, to time.Time) (SystemHistoryStats, error) {
	events, _ := d.ListEvents(ctx, sysID, from, to, 500)
	return d.GetSystemUptimeStatsWithEvents(ctx, sysID, from, to, events)
}

// GetSystemUptimeStatsWithEvents calculates uptime and latency stats reusing preloaded events.
func (d *DB) GetSystemUptimeStatsWithEvents(ctx context.Context, sysID string, from, to time.Time, events []StoredEvent) (SystemHistoryStats, error) {
	stats := SystemHistoryStats{
		DNSUptime: 100.0,
		IPUptime:  100.0,
	}

	query := `
SELECT kind, COUNT(*), SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END)
FROM checks
WHERE system_id = ? AND checked_at >= ? AND checked_at <= ?
GROUP BY kind
`
	rows, err := d.db.QueryContext(ctx, query, sysID, from.UTC(), to.UTC())
	if err != nil {
		return stats, fmt.Errorf("query uptime stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var kind string
		var total, okCount int
		if err := rows.Scan(&kind, &total, &okCount); err != nil {
			return stats, fmt.Errorf("scan uptime row: %w", err)
		}
		if total > 0 {
			ratio := (float64(okCount) / float64(total)) * 100.0
			if kind == "dns" {
				stats.DNSUptime = ratio
			} else if kind == "ip" {
				stats.IPUptime = ratio
			}
		}
	}

	evQuery := `
SELECT COUNT(*)
FROM events
WHERE system_id = ? AND to_status = 'DOWN' AND occurred_at >= ? AND occurred_at <= ?
`
	err = d.db.QueryRowContext(ctx, evQuery, sysID, from.UTC(), to.UTC()).Scan(&stats.IncidentCount)
	if err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("count incidents: %w", err)
	}

	latQuery := `
SELECT COALESCE(latency_ms, 0)
FROM checks
WHERE system_id = ? AND ok = 1 AND checked_at >= ? AND checked_at <= ?
ORDER BY latency_ms ASC
`
	latRows, err := d.db.QueryContext(ctx, latQuery, sysID, from.UTC(), to.UTC())
	if err == nil {
		defer latRows.Close()
		var latencies []int64
		var sumLat int64
		for latRows.Next() {
			var lat int64
			if scanErr := latRows.Scan(&lat); scanErr == nil {
				latencies = append(latencies, lat)
				sumLat += lat
			}
		}
		if len(latencies) > 0 {
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			stats.AvgLatencyMs = sumLat / int64(len(latencies))
			p95Idx := int(float64(len(latencies)-1) * 0.95)
			stats.P95LatencyMs = latencies[p95Idx]
		}
	}

	if len(events) > 0 {
		var totalDown time.Duration
		var resolvedCount int
		for _, ev := range events {
			if ev.Duration > 0 {
				totalDown += ev.Duration
				resolvedCount++
				if ev.Duration > stats.MaxDownDuration {
					stats.MaxDownDuration = ev.Duration
				}
			}
		}
		if resolvedCount > 0 {
			stats.MTTR = totalDown / time.Duration(resolvedCount)
		}
		if stats.IncidentCount > 0 {
			periodDur := to.Sub(from)
			upTime := periodDur - totalDown
			if upTime > 0 {
				stats.MTBF = upTime / time.Duration(stats.IncidentCount)
			}
		}
	}

	return stats, nil
}

// ListAllRecentEvents retrieves the most recent status transition events across all systems.
func (d *DB) ListAllRecentEvents(ctx context.Context, limit int) ([]StoredEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
SELECT id, system_id, kind, from_status, to_status, COALESCE(diagnosis, ''), COALESCE(detail, ''), occurred_at
FROM events
ORDER BY occurred_at DESC
LIMIT ?
`
	rows, err := d.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query all recent events: %w", err)
	}
	defer rows.Close()

	var events []StoredEvent
	for rows.Next() {
		var ev StoredEvent
		var occStr string
		if err := rows.Scan(
			&ev.ID,
			&ev.SystemID,
			&ev.Kind,
			&ev.FromStatus,
			&ev.ToStatus,
			&ev.Diagnosis,
			&ev.Detail,
			&occStr,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.OccurredAt, _ = parseTime(occStr)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// GetRecentChecks retrieves the most recent checks for a system and kind, ordered oldest-to-newest.
func (d *DB) GetRecentChecks(ctx context.Context, sysID, kind string, limit int) ([]checker.Result, error) {
	if limit <= 0 {
		limit = 120
	}

	query := `
SELECT ok, COALESCE(status_code, 0), COALESCE(latency_ms, 0), COALESCE(loss_pct, 0),
       COALESCE(error_cat, ''), COALESCE(error_msg, ''), checked_at
FROM checks
WHERE system_id = ? AND kind = ?
ORDER BY checked_at DESC
LIMIT ?
`
	rows, err := d.db.QueryContext(ctx, query, sysID, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent checks: %w", err)
	}
	defer rows.Close()

	var results []checker.Result
	for rows.Next() {
		var okInt int
		var statusCode int
		var latencyMs int64
		var lossPct float64
		var errCat, errMsg, checkStr string

		if err := rows.Scan(&okInt, &statusCode, &latencyMs, &lossPct, &errCat, &errMsg, &checkStr); err != nil {
			return nil, fmt.Errorf("scan recent check: %w", err)
		}

		checkedAt, _ := parseTime(checkStr)
		results = append(results, checker.Result{
			OK:         okInt == 1,
			StatusCode: statusCode,
			Latency:    time.Duration(latencyMs) * time.Millisecond,
			Loss:       lossPct,
			ErrorCat:   errCat,
			Error:      errMsg,
			CheckedAt:  checkedAt.Local(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse to chronological order (oldest first)
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}

	return results, nil
}

// GetLatestEventTime returns the timestamp of the most recent status transition for a system/kind.
func (d *DB) GetLatestEventTime(ctx context.Context, sysID, kind string) (time.Time, error) {
	query := `
SELECT occurred_at
FROM events
WHERE system_id = ? AND kind = ?
ORDER BY occurred_at DESC
LIMIT 1
`
	var occStr string
	err := d.db.QueryRowContext(ctx, query, sysID, kind).Scan(&occStr)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("query latest event time: %w", err)
	}
	t, err := parseTime(occStr)
	if err != nil {
		return time.Time{}, nil
	}
	return t.Local(), nil
}

// SetMaintenanceOverride persists a manual maintenance state override for a system.
func (d *DB) SetMaintenanceOverride(ctx context.Context, sysID string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := d.db.ExecContext(ctx, `
INSERT INTO maintenance_overrides (system_id, enabled, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(system_id) DO UPDATE SET
  enabled = excluded.enabled,
  updated_at = excluded.updated_at
`, sysID, val, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("upsert maintenance override: %w", err)
	}
	return nil
}

// GetMaintenanceOverrides loads all persisted maintenance overrides keyed by system ID.
func (d *DB) GetMaintenanceOverrides(ctx context.Context) (map[string]bool, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT system_id, enabled FROM maintenance_overrides`)
	if err != nil {
		return nil, fmt.Errorf("query maintenance overrides: %w", err)
	}
	defer rows.Close()

	overrides := make(map[string]bool)
	for rows.Next() {
		var sysID string
		var enabledInt int
		if err := rows.Scan(&sysID, &enabledInt); err != nil {
			return nil, fmt.Errorf("scan maintenance override: %w", err)
		}
		overrides[sysID] = enabledInt == 1
	}
	return overrides, rows.Err()
}

func parseTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}
