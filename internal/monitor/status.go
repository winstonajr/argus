package monitor

import (
	"time"

	"sentinela/internal/checker"
)

// Status represents the health state of a check or system.
type Status string

const (
	StatusUnknown  Status = "UNKNOWN"
	StatusUP       Status = "UP"
	StatusDegraded Status = "DEGRADED"
	StatusDown     Status = "DOWN"
)

// Severity returns an integer rank for worst-of calculation.
func (s Status) Severity() int {
	switch s {
	case StatusDown:
		return 3
	case StatusDegraded:
		return 2
	case StatusUnknown:
		return 1
	case StatusUP:
		return 0
	default:
		return 1
	}
}

// WorstStatus returns the most critical status between a and b.
func WorstStatus(a, b Status) Status {
	if a.Severity() >= b.Severity() {
		return a
	}
	return b
}

// ChangeEvent represents a transition between statuses.
type ChangeEvent struct {
	SystemID   string
	SystemName string
	Kind       string // "dns" or "ip"
	FromStatus Status
	ToStatus   Status
	Diagnosis  string
	Detail     string
	OccurredAt time.Time
}

// HeartbeatTick represents a single slot in the 30-slot heartbeat bar.
type HeartbeatTick struct {
	Status    Status
	Latency   time.Duration
	CheckedAt time.Time
	Tooltip   string
}

// CheckSnapshot is an immutable view of a check state for rendering.
type CheckSnapshot struct {
	Configured  bool
	Status      Status
	LastResult  checker.Result
	LastChange  time.Time
	ConsecFails int
	Uptime1h    float64
	HasHistory  bool
}

// SystemSnapshot is an immutable view of a system for templates and JSON.
type SystemSnapshot struct {
	ID             string
	Name           string
	URL            string
	IP             string
	TCPPort        int
	WorstStatus    Status
	Diagnosis      string
	DownDuration   time.Duration
	LastCheckedAt  time.Time
	LastChange     time.Time
	DNS            CheckSnapshot
	IPCheck        CheckSnapshot
	Heartbeat      []HeartbeatTick // last 30 checks
	LatencyHistory []int64         // last 15 latencies in ms for sparkline
	JustChanged    bool            // true if changed within last polling window
}

// StatsSnapshot provides aggregate dashboard statistics.
type StatsSnapshot struct {
	Total       int
	UpCount     int
	DegradedCount int
	DownCount   int
	UnknownCount int
	WorstStatus Status
	LastCheck   time.Time
	UptimeStart time.Time
	UptimeRatio float64
}
