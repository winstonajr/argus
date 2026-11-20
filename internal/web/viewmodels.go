package web

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"argus/internal/i18n"
	"argus/internal/monitor"
	"argus/internal/storage"
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
	IntervalMs           int64
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
	DateGroup           string
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
	DNSTooltip     string
	IPStatus       string
	IPStatusLower  string
	IPTooltip      string
	TimeFormatted  string
}

type latencyChartViewModel struct {
	HasData         bool
	PolylinePoints  string
	AreaPoints      string
	MaxLatencyMs    int64
	MidLatencyMs    int64
	HasDegradedLine bool
	DegradedLineY   string
	StartLabel      string
	EndLabel        string
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
		hbSummary = fmt.Sprintf(t.HeartbeatSummaryFmt, t.RecentHistoryAria, okTicks, totalDataTicks)
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

	intervalMs := snap.Interval.Milliseconds()
	if intervalMs <= 0 {
		intervalMs = 30000
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
		IntervalMs:           intervalMs,
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

func buildLatencyChart(buckets []storage.TimelineBucket, degradedLatencyMs int64, period string) latencyChartViewModel {
	type pt struct {
		idx int
		val int64
	}
	var pts []pt
	var maxVal int64
	for i, b := range buckets {
		if b.HasData && b.AvgLatencyMs > 0 {
			pts = append(pts, pt{idx: i, val: b.AvgLatencyMs})
			if b.AvgLatencyMs > maxVal {
				maxVal = b.AvgLatencyMs
			}
		}
	}
	if len(pts) < 2 {
		return latencyChartViewModel{HasData: false}
	}

	if maxVal < 10 {
		maxVal = 10
	}
	// Add 15% headroom
	ceilVal := int64(float64(maxVal) * 1.15)
	if ceilVal <= 0 {
		ceilVal = 10
	}

	const width = 600.0
	const height = 96.0
	const padTop = 8.0
	const plotH = height - padTop - 4.0

	total := len(buckets)
	if total < 2 {
		total = 2
	}

	var poly strings.Builder
	var firstX, lastX float64
	for i, p := range pts {
		x := float64(p.idx) / float64(total-1) * width
		y := padTop + plotH - (float64(p.val)/float64(ceilVal))*plotH
		if i == 0 {
			firstX = x
		} else {
			poly.WriteByte(' ')
		}
		lastX = x
		fmt.Fprintf(&poly, "%.1f,%.1f", x, y)
	}

	polyStr := poly.String()
	areaStr := fmt.Sprintf("%.1f,%.1f %s %.1f,%.1f", firstX, padTop+plotH, polyStr, lastX, padTop+plotH)

	var hasDeg bool
	var degYStr string
	if degradedLatencyMs > 0 && degradedLatencyMs <= ceilVal {
		hasDeg = true
		degY := padTop + plotH - (float64(degradedLatencyMs)/float64(ceilVal))*plotH
		degYStr = fmt.Sprintf("%.1f", degY)
	}

	timeFmt := "15:04"
	if period == "7d" || period == "30d" {
		timeFmt = "02/01 15:04"
	}

	return latencyChartViewModel{
		HasData:         true,
		PolylinePoints:  polyStr,
		AreaPoints:      areaStr,
		MaxLatencyMs:    ceilVal,
		MidLatencyMs:    ceilVal / 2,
		HasDegradedLine: hasDeg,
		DegradedLineY:   degYStr,
		StartLabel:      buckets[0].StartTime.Local().Format(timeFmt),
		EndLabel:        buckets[len(buckets)-1].EndTime.Local().Format(timeFmt),
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
