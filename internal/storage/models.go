package storage

import "time"

// StoredCheck represents a raw check log in database.
type StoredCheck struct {
	ID         int64     `json:"id"`
	SystemID   string    `json:"system_id"`
	Kind       string    `json:"kind"` // "dns" | "ip"
	OK         bool      `json:"ok"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	LossPct    float64   `json:"loss_pct"`
	ErrorCat   string    `json:"error_cat"`
	ErrorMsg   string    `json:"error_msg"`
	CheckedAt  time.Time `json:"checked_at"`
}

// MaintenanceOverride is a persisted manual maintenance state; Until is zero when it has no expiration.
type MaintenanceOverride struct {
	Enabled bool
	Until   time.Time
}

// StoredEvent represents a recorded status transition with diagnosis.
type StoredEvent struct {
	ID         int64         `json:"id"`
	SystemID   string        `json:"system_id"`
	Kind       string        `json:"kind"`
	FromStatus string        `json:"from_status"`
	ToStatus   string        `json:"to_status"`
	Diagnosis  string        `json:"diagnosis"`
	Detail     string        `json:"detail"`
	Note       string        `json:"note,omitempty"`
	PublicNote bool          `json:"public_note,omitempty"`
	OccurredAt time.Time     `json:"occurred_at"`
	Duration   time.Duration `json:"duration,omitempty"` // computed if recovered
}

// TimelineBucket represents aggregated status and latency for an interval in the timeline bar.
type TimelineBucket struct {
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	DNSStatus    string    `json:"dns_status"` // "UP", "DEGRADED", "DOWN", "UNKNOWN"
	IPStatus     string    `json:"ip_status"`
	AvgLatencyMs int64     `json:"avg_latency_ms"`
	MaxLatencyMs int64     `json:"max_latency_ms"`
	TotalChecks  int       `json:"total_checks"`
	OKChecks     int       `json:"ok_checks"`
	HasData      bool      `json:"has_data"`
}

// DailyUptimeBucket represents one day of aggregated availability for the public status page.
type DailyUptimeBucket struct {
	Day       time.Time `json:"day"`
	Status    string    `json:"status"` // "UP", "DEGRADED", "DOWN", "UNKNOWN"
	UptimePct float64   `json:"uptime_pct"`
	HasData   bool      `json:"has_data"`
}

// SystemHistoryStats summarizes incident count and uptime for a period.
type SystemHistoryStats struct {
	DNSUptime       float64       `json:"dns_uptime"`
	IPUptime        float64       `json:"ip_uptime"`
	IncidentCount   int           `json:"incident_count"`
	MaxDownDuration time.Duration `json:"max_down_duration"`
	AvgLatencyMs    int64         `json:"avg_latency_ms"`
	P95LatencyMs    int64         `json:"p95_latency_ms"`
	AvgDNSMs        int64         `json:"avg_dns_ms"`
	AvgConnectMs    int64         `json:"avg_connect_ms"`
	AvgTLSMs        int64         `json:"avg_tls_ms"`
	AvgTTFBMs       int64         `json:"avg_ttfb_ms"`
	MTTR            time.Duration `json:"mttr"`
	MTBF            time.Duration `json:"mtbf"`
}
