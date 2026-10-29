package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
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

	// Calculate recovery duration where applicable
	for i := range events {
		if events[i].ToStatus == "UP" && i+1 < len(events) && events[i+1].ToStatus == "DOWN" {
			events[i].Duration = events[i].OccurredAt.Sub(events[i+1].OccurredAt)
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
SELECT kind, ok, COALESCE(loss_pct, 0), checked_at
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
		var loss float64
		var checkStr string

		if err := rows.Scan(&kind, &okInt, &loss, &checkStr); err != nil {
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
			}
		} else if kind == "ip" {
			if okInt == 0 {
				b.IPStatus = "DOWN"
			} else if loss > 0 && b.IPStatus != "DOWN" {
				b.IPStatus = "DEGRADED"
			}
		}
	}

	return buckets, rows.Err()
}

// GetSystemUptimeStats calculates DNS and IP uptime percentage and incident counts in range.
func (d *DB) GetSystemUptimeStats(ctx context.Context, sysID string, from, to time.Time) (SystemHistoryStats, error) {
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

	// Count incidents (number of transitions into DOWN)
	evQuery := `
SELECT COUNT(*)
FROM events
WHERE system_id = ? AND to_status = 'DOWN' AND occurred_at >= ? AND occurred_at <= ?
`
	err = d.db.QueryRowContext(ctx, evQuery, sysID, from.UTC(), to.UTC()).Scan(&stats.IncidentCount)
	if err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("count incidents: %w", err)
	}

	return stats, nil
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
