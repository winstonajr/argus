package web

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"argus/internal/i18n"
	"argus/internal/monitor"
)

type cardViewModel struct {
	T                    i18n.Messages
	ID                   string
	Name                 string
	Group                string
	URL                  string
	DisplayURL           string
	IP                   string
	DisplayIP            string
	IsIPHTTP             bool
	TCPPort              int
	WorstStatus          string
	WorstStatusLower     string
	DNSStatusLower       string
	IPStatusLower        string
	DNSLatencyMs         int64
	IPLatencyMs          int64
	Uptime1h             float64
	Diagnosis            string
	DownDurationStr      string
	LastCheckedFormatted string
	LastCheckedTimeOnly  string
	LastCheckedUnixMs    int64
	DNS                  monitor.CheckSnapshot
	IPCheck              monitor.CheckSnapshot
	Heartbeat            []heartbeatViewModel
	HeartbeatSummary     string
	SparklinePoints      string
	HasSparkline         bool
	HasTLS               bool
	TLSDaysLeft          int
	TLSExpiringSoon      bool
	InMaintenance        bool
	JustChanged          bool
}

type heartbeatViewModel struct {
	StatusLower string
	Tooltip     string
}

type eventViewModel struct {
	Kind                string
	FromStatus          string
	ToStatus            string
	ToStatusLower       string
	Diagnosis           string
	Detail              string
	OccurredAtFormatted string
	DurationStr         string
}

type failedCheckViewModel struct {
	Kind               string
	StatusCode         int
	LatencyMs          int64
	ErrorCat           string
	ErrorMsg           string
	CheckedAtFormatted string
}

type timelineBucketViewModel struct {
	DNSStatus      string
	DNSStatusLower string
	IPStatus       string
	IPStatusLower  string
	TimeFormatted  string
}

func toCardViewModel(snap monitor.SystemSnapshot) cardViewModel {
	return toCardViewModelWithLang(snap, i18n.Current())
}

func toCardViewModelWithLang(snap monitor.SystemSnapshot, t i18n.Messages) cardViewModel {
	dnsLower := strings.ToLower(string(snap.DNS.Status))
	ipLower := strings.ToLower(string(snap.IPCheck.Status))
	worstLower := strings.ToLower(string(snap.WorstStatus))

	displayURL := snap.URL
	if u, err := url.Parse(snap.URL); err == nil && u.Host != "" {
		displayURL = u.Host
	}

	displayIP := snap.IP
	if snap.IsIPHTTP {
		if u, err := url.Parse(snap.IP); err == nil && u.Host != "" {
			displayIP = u.Host
		}
	}

	lastCheckStr := ""
	lastCheckTimeOnly := ""
	var lastCheckUnixMs int64
	if !snap.LastCheckedAt.IsZero() {
		localTime := snap.LastCheckedAt.Local()
		lastCheckStr = localTime.Format("02/01/2006 15:04:05")
		lastCheckTimeOnly = localTime.Format("15:04:05")
		lastCheckUnixMs = snap.LastCheckedAt.UnixMilli()
	}

	hbVM := make([]heartbeatViewModel, len(snap.Heartbeat))
	okTicks := 0
	totalDataTicks := 0
	for i, tick := range snap.Heartbeat {
		stLower := strings.ToLower(string(tick.Status))
		hbVM[i] = heartbeatViewModel{
			StatusLower: stLower,
			Tooltip:     tick.Tooltip,
		}
		if tick.Status != monitor.StatusUnknown {
			totalDataTicks++
			if tick.Status == monitor.StatusUP {
				okTicks++
			}
		}
	}

	hbSummary := t.RecentHistoryAria
	if totalDataTicks > 0 {
		if t.Lang == i18n.LangEN {
			hbSummary = fmt.Sprintf("%s: %d of %d checks operational", t.RecentHistoryAria, okTicks, totalDataTicks)
		} else {
			hbSummary = fmt.Sprintf("%s: %d de %d verificações operacionais", t.RecentHistoryAria, okTicks, totalDataTicks)
		}
	}

	downDurStr := ""
	if snap.DownDuration > 0 {
		downDurStr = formatDurationShort(snap.DownDuration)
	}

	sparkPoints := buildSparklinePoints(snap.LatencyHistory)

	hasTLS := snap.DNS.LastResult.HasTLS
	tlsDaysLeft := snap.DNS.LastResult.TLSDaysLeft
	tlsWarnDays := snap.DNS.TLSWarnDays
	if !hasTLS && snap.IPCheck.LastResult.HasTLS {
		hasTLS = true
		tlsDaysLeft = snap.IPCheck.LastResult.TLSDaysLeft
		tlsWarnDays = snap.IPCheck.TLSWarnDays
	}
	if tlsWarnDays <= 0 {
		tlsWarnDays = 15
	}

	return cardViewModel{
		T:                    t,
		ID:                   snap.ID,
		Name:                 snap.Name,
		Group:                snap.Group,
		URL:                  snap.URL,
		DisplayURL:           displayURL,
		IP:                   snap.IP,
		DisplayIP:            displayIP,
		IsIPHTTP:             snap.IsIPHTTP,
		TCPPort:              snap.TCPPort,
		WorstStatus:          string(snap.WorstStatus),
		WorstStatusLower:     worstLower,
		DNSStatusLower:       dnsLower,
		IPStatusLower:        ipLower,
		DNSLatencyMs:         snap.DNS.LastResult.Latency.Milliseconds(),
		IPLatencyMs:          snap.IPCheck.LastResult.Latency.Milliseconds(),
		Uptime1h:             snap.Uptime1h,
		Diagnosis:            snap.Diagnosis,
		DownDurationStr:      downDurStr,
		LastCheckedFormatted: lastCheckStr,
		LastCheckedTimeOnly:  lastCheckTimeOnly,
		LastCheckedUnixMs:    lastCheckUnixMs,
		DNS:                  snap.DNS,
		IPCheck:              snap.IPCheck,
		Heartbeat:            hbVM,
		HeartbeatSummary:     hbSummary,
		SparklinePoints:      sparkPoints,
		HasSparkline:         sparkPoints != "",
		HasTLS:               hasTLS,
		TLSDaysLeft:          tlsDaysLeft,
		TLSExpiringSoon:      hasTLS && tlsDaysLeft < tlsWarnDays,
		InMaintenance:        snap.InMaintenance,
		JustChanged:          snap.JustChanged,
	}
}

func buildSparklinePoints(latencies []int64) string {
	n := len(latencies)
	if n < 2 {
		return ""
	}
	minVal, maxVal := latencies[0], latencies[0]
	for _, v := range latencies[1:] {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	const width = 60.0
	const height = 14.0
	const padY = 1.0

	var b strings.Builder
	span := float64(maxVal - minVal)
	for i, v := range latencies {
		x := float64(i) / float64(n-1) * width
		var y float64
		if span == 0 {
			y = padY + height/2.0
		} else {
			norm := float64(v-minVal) / span
			y = padY + height - (norm * height)
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
	}
	return b.String()
}

func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
