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
SELECT id, system_id, kind, from_status, to_status, COALESCE(diagnosis, ''), COALESCE(detail, ''), occurred_at_ms, occurred_at
FROM events
WHERE system_id = ? AND occurred_at_ms >= ? AND occurred_at_ms <= ?
ORDER BY occurred_at_ms DESC, id DESC
LIMIT ?
`
	rows, err := d.db.QueryContext(ctx, query, sysID, from.UTC().UnixMilli(), to.UTC().UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []StoredEvent
	for rows.Next() {
		var ev StoredEvent
		var occMs int64
		var occStr string
		err := rows.Scan(
			&ev.ID,
			&ev.SystemID,
			&ev.Kind,
			&ev.FromStatus,
			&ev.ToStatus,
			&ev.Diagnosis,
			&ev.Detail,
			&occMs,
			&occStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if occMs > 0 {
			ev.OccurredAt = time.UnixMilli(occMs).UTC()
		} else {
			ev.OccurredAt, _ = parseTime(occStr)
		}
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
       COALESCE(error_cat, ''), COALESCE(error_msg, ''), checked_at_ms, checked_at
FROM checks
WHERE system_id = ? AND ok = 0 AND checked_at_ms >= ? AND checked_at_ms <= ?
ORDER BY checked_at_ms DESC, id DESC
LIMIT ? OFFSET ?
`
	rows, err := d.db.QueryContext(ctx, query, sysID, from.UTC().UnixMilli(), to.UTC().UnixMilli(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query failed checks: %w", err)
	}
	defer rows.Close()

	var checks []StoredCheck
	for rows.Next() {
		var c StoredCheck
		var okInt int
		var checkMs int64
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
			&checkMs,
			&checkStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan failed check: %w", err)
		}
		c.OK = okInt == 1
		if checkMs > 0 {
			c.CheckedAt = time.UnixMilli(checkMs).UTC()
		} else {
			c.CheckedAt, _ = parseTime(checkStr)
		}
		checks = append(checks, c)
	}

	return checks, rows.Err()
}

// GetTimelineBuckets computes slice of time buckets and their worst status.
func (d *DB) GetTimelineBuckets(ctx context.Context, sysID string, from, to time.Time, bucketCount int) ([]TimelineBucket, error) {
	return d.GetTimelineBucketsWithLatency(ctx, sysID, from, to, bucketCount, 2000)
}

// GetTimelineBucketsWithLatency computes time buckets in SQL (from raw checks for <=48h or hourly rollups for >48h).
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

	fromMs := from.UTC().UnixMilli()
	toMs := to.UTC().UnixMilli()
	bucketMs := (toMs - fromMs) / int64(bucketCount)
	if bucketMs <= 0 {
		bucketMs = 1
	}

	var rows *sql.Rows
	var err error
	if totalDuration <= 48*time.Hour {
		query := `
SELECT
  CAST((checked_at_ms - ?) / ? AS INTEGER) AS b_idx,
  kind,
  COUNT(*),
  SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END),
  SUM(CASE WHEN ok = 1 AND (COALESCE(loss_pct, 0) > 0 OR (? > 0 AND COALESCE(latency_ms, 0) > ?)) THEN 1 ELSE 0 END),
  SUM(CASE WHEN ok = 1 THEN COALESCE(latency_ms, 0) ELSE 0 END),
  MAX(COALESCE(latency_ms, 0))
FROM checks
WHERE system_id = ? AND checked_at_ms >= ? AND checked_at_ms < ?
GROUP BY b_idx, kind
`
		rows, err = d.db.QueryContext(ctx, query, fromMs, bucketMs, degradedLatencyMs, degradedLatencyMs, sysID, fromMs, toMs)
	} else {
		fromHourMs := (fromMs / hourMillis) * hourMillis
		query := `
SELECT
  CAST((hour_ms - ?) / ? AS INTEGER) AS b_idx,
  kind,
  SUM(total),
  SUM(ok_count),
  SUM(degraded_count),
  SUM(sum_latency_ms),
  MAX(max_latency_ms)
FROM checks_hourly
WHERE system_id = ? AND hour_ms >= ? AND hour_ms <= ?
GROUP BY b_idx, kind
`
		rows, err = d.db.QueryContext(ctx, query, fromMs, bucketMs, sysID, fromHourMs, toMs)
	}
	if err != nil {
		return nil, fmt.Errorf("query timeline buckets: %w", err)
	}
	defer rows.Close()

	latSums := make([]int64, bucketCount)
	latCounts := make([]int64, bucketCount)

	for rows.Next() {
		var idx int
		var kind string
		var total, okCount, degCount int
		var sumLat, maxLat int64

		if err := rows.Scan(&idx, &kind, &total, &okCount, &degCount, &sumLat, &maxLat); err != nil {
			return nil, fmt.Errorf("scan timeline bucket: %w", err)
		}
		if total <= 0 {
			continue
		}
		if idx < 0 {
			idx = 0
		} else if idx >= bucketCount {
			idx = bucketCount - 1
		}

		b := &buckets[idx]
		b.HasData = true
		b.TotalChecks += total
		b.OKChecks += okCount
		if maxLat > b.MaxLatencyMs {
			b.MaxLatencyMs = maxLat
		}
		if okCount > 0 {
			latSums[idx] += sumLat
			latCounts[idx] += int64(okCount)
		}

		status := "UP"
		if okCount < total {
			status = "DOWN"
		} else if degCount > 0 || (degradedLatencyMs > 0 && maxLat > degradedLatencyMs) {
			status = "DEGRADED"
		}

		if kind == "dns" {
			b.DNSStatus = mergeBucketStatus(b.DNSStatus, status)
		} else if kind == "ip" {
			b.IPStatus = mergeBucketStatus(b.IPStatus, status)
		}
	}

	for i := range buckets {
		if latCounts[i] > 0 {
			buckets[i].AvgLatencyMs = latSums[i] / latCounts[i]
		}
	}

	return buckets, rows.Err()
}

func mergeBucketStatus(cur, next string) string {
	if cur == "DOWN" || next == "DOWN" {
		return "DOWN"
	}
	if cur == "DEGRADED" || next == "DEGRADED" {
		return "DEGRADED"
	}
	return "UP"
}

// GetDailyUptimeBuckets returns up to `days` daily availability buckets from checks_hourly for a system.
func (d *DB) GetDailyUptimeBuckets(ctx context.Context, sysID string, days int) ([]DailyUptimeBucket, error) {
	if days <= 0 {
		days = 30
	}
	nowUTC := time.Now().UTC()
	todayStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	startDay := todayStart.AddDate(0, 0, -(days - 1))
	fromMs := startDay.UnixMilli()
	const dayMillis = 24 * hourMillis

	res := make([]DailyUptimeBucket, days)
	for i := 0; i < days; i++ {
		res[i] = DailyUptimeBucket{
			Day:       startDay.AddDate(0, 0, i),
			Status:    "UNKNOWN",
			UptimePct: 100.0,
			HasData:   false,
		}
	}

	query := `
SELECT
  CAST((hour_ms - ?) / ? AS INTEGER) AS d_idx,
  SUM(total),
  SUM(ok_count),
  SUM(degraded_count)
FROM checks_hourly
WHERE system_id = ? AND hour_ms >= ?
GROUP BY d_idx
`
	rows, err := d.db.QueryContext(ctx, query, fromMs, dayMillis, sysID, fromMs)
	if err != nil {
		return res, fmt.Errorf("query daily uptime: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var idx, total, okCount, degCount int
		if err := rows.Scan(&idx, &total, &okCount, &degCount); err != nil {
			return res, err
		}
		if idx < 0 || idx >= days || total <= 0 {
			continue
		}
		pct := float64(okCount) / float64(total) * 100.0
		st := "UP"
		if pct < 95.0 {
			st = "DOWN"
		} else if pct < 100.0 || degCount > 0 {
			st = "DEGRADED"
		}
		res[idx].HasData = true
		res[idx].UptimePct = pct
		res[idx].Status = st
	}

	return res, rows.Err()
}

// GetSystemUptimeStats calculates DNS and IP uptime percentage and incident counts in range.
func (d *DB) GetSystemUptimeStats(ctx context.Context, sysID string, from, to time.Time) (SystemHistoryStats, error) {
	events, err := d.ListEvents(ctx, sysID, from, to, 1000)
	if err != nil {
		return SystemHistoryStats{}, err
	}
	return d.GetSystemUptimeStatsWithEvents(ctx, sysID, from, to, events)
}

// GetSystemUptimeStatsWithEvents calculates uptime and latency stats reusing preloaded events.
func (d *DB) GetSystemUptimeStatsWithEvents(ctx context.Context, sysID string, from, to time.Time, events []StoredEvent) (SystemHistoryStats, error) {
	stats := SystemHistoryStats{
		DNSUptime: 100.0,
		IPUptime:  100.0,
	}

	fromMs := from.UTC().UnixMilli()
	toMs := to.UTC().UnixMilli()
	useHourly := to.Sub(from) > 48*time.Hour

	var rows *sql.Rows
	var err error
	if useHourly {
		fromHourMs := (fromMs / hourMillis) * hourMillis
		query := `
SELECT kind, SUM(total), SUM(ok_count), SUM(sum_latency_ms)
FROM checks_hourly
WHERE system_id = ? AND hour_ms >= ? AND hour_ms <= ?
GROUP BY kind
`
		rows, err = d.db.QueryContext(ctx, query, sysID, fromHourMs, toMs)
	} else {
		query := `
SELECT kind, COUNT(*), SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END), SUM(CASE WHEN ok = 1 THEN COALESCE(latency_ms, 0) ELSE 0 END)
FROM checks
WHERE system_id = ? AND checked_at_ms >= ? AND checked_at_ms <= ?
GROUP BY kind
`
		rows, err = d.db.QueryContext(ctx, query, sysID, fromMs, toMs)
	}
	if err != nil {
		return stats, fmt.Errorf("query uptime stats: %w", err)
	}
	defer rows.Close()

	var totalOKCount int64
	var totalOKLatSum int64
	for rows.Next() {
		var kind string
		var total, okCount int64
		var sumLat int64
		if err := rows.Scan(&kind, &total, &okCount, &sumLat); err != nil {
			return stats, fmt.Errorf("scan uptime row: %w", err)
		}
		if total > 0 {
			ratio := (float64(okCount) / float64(total)) * 100.0
			if kind == "dns" {
				stats.DNSUptime = ratio
			} else if kind == "ip" {
				stats.IPUptime = ratio
			}
			totalOKCount += okCount
			totalOKLatSum += sumLat
		}
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("iterate uptime rows: %w", err)
	}
	if totalOKCount > 0 {
		stats.AvgLatencyMs = totalOKLatSum / totalOKCount
	}

	evQuery := `
SELECT COUNT(*)
FROM events
WHERE system_id = ? AND to_status = 'DOWN' AND occurred_at_ms >= ? AND occurred_at_ms <= ?
`
	err = d.db.QueryRowContext(ctx, evQuery, sysID, fromMs, toMs).Scan(&stats.IncidentCount)
	if err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("count incidents: %w", err)
	}

	var rawOKCount int64
	if err := d.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM checks
WHERE system_id = ? AND ok = 1 AND checked_at_ms >= ? AND checked_at_ms <= ?
`, sysID, fromMs, toMs).Scan(&rawOKCount); err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("count ok checks for p95: %w", err)
	}

	if rawOKCount > 0 {
		p95Offset := int64(float64(rawOKCount-1) * 0.95)
		if err := d.db.QueryRowContext(ctx, `
SELECT COALESCE(latency_ms, 0)
FROM checks
WHERE system_id = ? AND ok = 1 AND checked_at_ms >= ? AND checked_at_ms <= ?
ORDER BY latency_ms ASC
LIMIT 1 OFFSET ?
`, sysID, fromMs, toMs, p95Offset).Scan(&stats.P95LatencyMs); err != nil && err != sql.ErrNoRows {
			return stats, fmt.Errorf("query p95 latency: %w", err)
		}
	}

	if len(events) > 0 {
		var totalDown time.Duration
		var resolvedCount int
		type downWindow struct {
			start, end time.Time
		}
		var windows []downWindow
		for _, ev := range events {
			if ev.Duration > 0 {
				totalDown += ev.Duration
				resolvedCount++
				if ev.Duration > stats.MaxDownDuration {
					stats.MaxDownDuration = ev.Duration
				}
				if !ev.OccurredAt.IsZero() {
					windows = append(windows, downWindow{
						start: ev.OccurredAt.Add(-ev.Duration),
						end:   ev.OccurredAt,
					})
				}
			}
		}
		if resolvedCount > 0 {
			stats.MTTR = totalDown / time.Duration(resolvedCount)
		}
		if stats.IncidentCount > 0 {
			mergedDown := totalDown
			if len(windows) > 0 {
				sort.Slice(windows, func(i, j int) bool {
					return windows[i].start.Before(windows[j].start)
				})
				mergedDown = 0
				curStart, curEnd := windows[0].start, windows[0].end
				for _, w := range windows[1:] {
					if !w.start.After(curEnd) {
						if w.end.After(curEnd) {
							curEnd = w.end
						}
					} else {
						mergedDown += curEnd.Sub(curStart)
						curStart, curEnd = w.start, w.end
					}
				}
				mergedDown += curEnd.Sub(curStart)
			}
			periodDur := to.Sub(from)
			upTime := periodDur - mergedDown
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
SELECT id, system_id, kind, from_status, to_status, COALESCE(diagnosis, ''), COALESCE(detail, ''), occurred_at_ms, occurred_at
FROM events
ORDER BY occurred_at_ms DESC, id DESC
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
		var occMs int64
		var occStr string
		if err := rows.Scan(
			&ev.ID,
			&ev.SystemID,
			&ev.Kind,
			&ev.FromStatus,
			&ev.ToStatus,
			&ev.Diagnosis,
			&ev.Detail,
			&occMs,
			&occStr,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if occMs > 0 {
			ev.OccurredAt = time.UnixMilli(occMs).UTC()
		} else {
			ev.OccurredAt, _ = parseTime(occStr)
		}
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
       COALESCE(error_cat, ''), COALESCE(error_msg, ''), checked_at_ms, checked_at
FROM checks
WHERE system_id = ? AND kind = ?
ORDER BY checked_at_ms DESC, id DESC
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
		var checkMs int64

		if err := rows.Scan(&okInt, &statusCode, &latencyMs, &lossPct, &errCat, &errMsg, &checkMs, &checkStr); err != nil {
			return nil, fmt.Errorf("scan recent check: %w", err)
		}

		var checkedAt time.Time
		if checkMs > 0 {
			checkedAt = time.UnixMilli(checkMs).Local()
		} else {
			t, _ := parseTime(checkStr)
			checkedAt = t.Local()
		}
		results = append(results, checker.Result{
			OK:         okInt == 1,
			StatusCode: statusCode,
			Latency:    time.Duration(latencyMs) * time.Millisecond,
			Loss:       lossPct,
			ErrorCat:   errCat,
			Error:      errMsg,
			CheckedAt:  checkedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}

	return results, nil
}

// GetRecentChecksBatch loads up to limitPerKey recent checks across all systems in a single query.
func (d *DB) GetRecentChecksBatch(ctx context.Context, limitPerKey int) (map[string][]checker.Result, error) {
	if limitPerKey <= 0 {
		limitPerKey = 120
	}

	query := `
SELECT system_id, kind, ok, status_code, latency_ms, loss_pct, error_cat, error_msg, checked_at_ms
FROM (
  SELECT
    system_id,
    kind,
    ok,
    COALESCE(status_code, 0) AS status_code,
    COALESCE(latency_ms, 0)  AS latency_ms,
    COALESCE(loss_pct, 0)    AS loss_pct,
    COALESCE(error_cat, '')  AS error_cat,
    COALESCE(error_msg, '')  AS error_msg,
    checked_at_ms,
    ROW_NUMBER() OVER (PARTITION BY system_id, kind ORDER BY checked_at_ms DESC, id DESC) AS rn
  FROM checks
)
WHERE rn <= ?
ORDER BY system_id, kind, checked_at_ms ASC, rn DESC
`
	rows, err := d.db.QueryContext(ctx, query, limitPerKey)
	if err != nil {
		return nil, fmt.Errorf("query recent checks batch: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]checker.Result)
	for rows.Next() {
		var sysID, kind, errCat, errMsg string
		var okInt, statusCode int
		var latencyMs, checkMs int64
		var lossPct float64

		if err := rows.Scan(&sysID, &kind, &okInt, &statusCode, &latencyMs, &lossPct, &errCat, &errMsg, &checkMs); err != nil {
			return nil, fmt.Errorf("scan recent check batch: %w", err)
		}
		key := sysID + ":" + kind
		out[key] = append(out[key], checker.Result{
			OK:         okInt == 1,
			StatusCode: statusCode,
			Latency:    time.Duration(latencyMs) * time.Millisecond,
			Loss:       lossPct,
			ErrorCat:   errCat,
			Error:      errMsg,
			CheckedAt:  time.UnixMilli(checkMs).Local(),
		})
	}
	return out, rows.Err()
}

// GetLatestEventTimesBatch returns the latest status transition timestamp per "system_id:kind" in one query.
func (d *DB) GetLatestEventTimesBatch(ctx context.Context) (map[string]time.Time, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT system_id, kind, MAX(occurred_at_ms)
FROM events
WHERE occurred_at_ms > 0
GROUP BY system_id, kind
`)
	if err != nil {
		return nil, fmt.Errorf("query latest event times batch: %w", err)
	}
	defer rows.Close()

	out := make(map[string]time.Time)
	for rows.Next() {
		var sysID, kind string
		var maxMs int64
		if err := rows.Scan(&sysID, &kind, &maxMs); err != nil {
			return nil, err
		}
		if maxMs > 0 {
			out[sysID+":"+kind] = time.UnixMilli(maxMs).Local()
		}
	}
	return out, rows.Err()
}

// GetLatestEventTime returns the timestamp of the most recent status transition for a system/kind.
func (d *DB) GetLatestEventTime(ctx context.Context, sysID, kind string) (time.Time, error) {
	query := `
SELECT occurred_at_ms, occurred_at
FROM events
WHERE system_id = ? AND kind = ?
ORDER BY occurred_at_ms DESC, id DESC
LIMIT 1
`
	var occMs int64
	var occStr string
	err := d.db.QueryRowContext(ctx, query, sysID, kind).Scan(&occMs, &occStr)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("query latest event time: %w", err)
	}
	if occMs > 0 {
		return time.UnixMilli(occMs).Local(), nil
	}
	t, err := parseTime(occStr)
	if err != nil {
		return time.Time{}, nil
	}
	return t.Local(), nil
}

// SetMaintenanceOverride persists a manual maintenance state override for a system.
func (d *DB) SetMaintenanceOverride(ctx context.Context, sysID string, enabled bool) error {
	return d.SetMaintenanceOverrideUntil(ctx, sysID, enabled, time.Time{})
}

// SetMaintenanceOverrideUntil persists a manual maintenance override with an optional expiration time.
func (d *DB) SetMaintenanceOverrideUntil(ctx context.Context, sysID string, enabled bool, until time.Time) error {
	val := 0
	var untilMs int64
	if enabled {
		val = 1
		if !until.IsZero() {
			untilMs = until.UTC().UnixMilli()
		}
	}
	_, err := d.db.ExecContext(ctx, `
INSERT INTO maintenance_overrides (system_id, enabled, until_ms, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(system_id) DO UPDATE SET
  enabled    = excluded.enabled,
  until_ms   = excluded.until_ms,
  updated_at = excluded.updated_at
`, sysID, val, untilMs, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert maintenance override: %w", err)
	}
	return nil
}

// GetMaintenanceOverrides loads all persisted maintenance overrides keyed by system ID.
func (d *DB) GetMaintenanceOverrides(ctx context.Context) (map[string]MaintenanceOverride, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT system_id, enabled, COALESCE(until_ms, 0) FROM maintenance_overrides`)
	if err != nil {
		return nil, fmt.Errorf("query maintenance overrides: %w", err)
	}
	defer rows.Close()

	nowMs := time.Now().UTC().UnixMilli()
	overrides := make(map[string]MaintenanceOverride)
	for rows.Next() {
		var sysID string
		var enabledInt int
		var untilMs int64
		if err := rows.Scan(&sysID, &enabledInt, &untilMs); err != nil {
			return nil, fmt.Errorf("scan maintenance override: %w", err)
		}
		ov := MaintenanceOverride{Enabled: enabledInt == 1}
		if ov.Enabled && untilMs > 0 {
			if nowMs >= untilMs {
				ov.Enabled = false
			} else {
				ov.Until = time.UnixMilli(untilMs)
			}
		}
		overrides[sysID] = ov
	}
	return overrides, rows.Err()
}

func parseTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
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
