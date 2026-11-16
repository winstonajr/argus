package checker

import "time"

// Result categories for diagnosis.
const (
	CatNone             = ""
	CatDNS              = "dns"
	CatConnect          = "connect"
	CatTLS              = "tls"
	CatTimeout          = "timeout"
	CatHTTP5xx          = "http_5xx"
	CatProxy            = "proxy"
	CatUnexpectedStatus = "unexpected_status"
	CatUnexpectedBody   = "unexpected_body"
	CatICMPTimeout      = "icmp_timeout"
	CatICMPLoss         = "icmp_loss"
	CatTCPRefused       = "tcp_refused"
	CatPermission       = "permission"
)

// Result holds the outcome of a single check execution.
type Result struct {
	OK          bool          `json:"ok"`
	StatusCode  int           `json:"status_code,omitempty"`
	Latency     time.Duration `json:"latency"`
	Loss        float64       `json:"loss,omitempty"` // percentage 0..100
	HasTLS      bool          `json:"has_tls,omitempty"`
	TLSDaysLeft int           `json:"tls_days_left,omitempty"`
	ErrorCat    string        `json:"error_cat,omitempty"`
	Error       string        `json:"error,omitempty"`
	CheckedAt   time.Time     `json:"checked_at"`
}
