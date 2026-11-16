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

// StoredEvent represents a recorded status transition with diagnosis.
type StoredEvent struct {
	ID         int64     `json:"id"`
	SystemID   string    `json:"system_id"`
	Kind       string    `json:"kind"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Diagnosis  string    `json:"diagnosis"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
	Duration   time.Duration `json:"duration,omitempty"` // computed if recovered
}

// TimelineBucket represents aggregated status for an interval in the timeline bar.
type TimelineBucket struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	DNSStatus string    `json:"dns_status"` // "UP", "DEGRADED", "DOWN", "UNKNOWN"
	IPStatus  string    `json:"ip_status"`
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
	MTTR            time.Duration `json:"mttr"`
	MTBF            time.Duration `json:"mtbf"`
}
