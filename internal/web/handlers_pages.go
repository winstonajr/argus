package web

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"argus/internal/i18n"
	"argus/internal/monitor"
	"argus/internal/storage"
)

func parsePeriod(raw string, t i18n.Messages) (period, label string, duration time.Duration, bucketCount int) {
	switch raw {
	case "1h":
		return "1h", t.Period1hLabel, 1 * time.Hour, 30
	case "7d":
		return "7d", t.Period7dLabel, 7 * 24 * time.Hour, 84
	case "30d":
		return "30d", t.Period30dLabel, 30 * 24 * time.Hour, 90
	default:
		return "24h", t.Period24hLabel, 24 * time.Hour, 60
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	systems := s.store.GetAllSystems()

	groupSet := make(map[string]struct{})
	for _, sys := range systems {
		if g := strings.TrimSpace(sys.Group); g != "" {
			groupSet[g] = struct{}{}
		}
	}
	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	statsHTML, err := s.renderStatsHTML(t)
	if err != nil {
		log.Printf("[ERROR] render index stats: %v", err)
	}

	var cardsBuf bytes.Buffer
	for _, sys := range systems {
		vm := toCardViewModelWithLang(sys, t)
		vm.JustChanged = false
		if err := s.cardTmpl.Execute(&cardsBuf, vm); err != nil {
			log.Printf("[ERROR] render index card %s: %v", sys.ID, err)
		}
	}

	connSt := s.connectivityStatus()

	data := struct {
		T                    i18n.Messages
		Title                string
		IconsSVG             template.HTML
		HideSearch           bool
		HideNOCControls      bool
		SantaEasterEgg       bool
		SeasonalThemes       bool
		Offline              bool
		OfflineLastCheckText string
		Groups               []string
		StatsHTML            template.HTML
		CardsHTML            template.HTML
	}{
		T:                    t,
		Title:                t.DashboardTitle,
		IconsSVG:             s.iconsSVG,
		HideSearch:           false,
		HideNOCControls:      false,
		SantaEasterEgg:       s.santaEasterEggEnabled(),
		SeasonalThemes:       s.seasonalThemesEnabled(),
		Offline:              connSt.Offline,
		OfflineLastCheckText: formatOfflineLastCheck(connSt.LastChecked, t),
		Groups:               groups,
		StatsHTML:            template.HTML(statsHTML),         //nolint:gosec
		CardsHTML:            template.HTML(cardsBuf.String()), //nolint:gosec
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render index: %v", err)
	}
}

func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, "system not found", http.StatusNotFound)
		return
	}

	vm := toCardViewModelWithLang(snap, t)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.cardTmpl.Execute(w, vm); err != nil {
		log.Printf("[ERROR] render card %s: %v", id, err)
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	htmlStr, err := s.renderStatsHTML(t)
	if err != nil {
		log.Printf("[ERROR] render stats: %v", err)
		http.Error(w, "failed to render stats", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(htmlStr))
}

func (s *Server) renderStatsHTML(t i18n.Messages) (string, error) {
	stats := s.store.GetSummaryCounts()

	lastCheckStr := "—"
	if !stats.LastCheck.IsZero() {
		lastCheckStr = stats.LastCheck.Local().Format("15:04:05")
	}

	uptimeDur := time.Since(stats.UptimeStart).Truncate(time.Second)
	uptimeStr := formatDurationShort(uptimeDur)

	data := struct {
		T                       i18n.Messages
		UpCount                 int
		DegradedCount           int
		DownCount               int
		Total                   int
		WorstStatus             string
		LastCheckFormatted      string
		UptimeDurationFormatted string
	}{
		T:                       t,
		UpCount:                 stats.UpCount,
		DegradedCount:           stats.DegradedCount,
		DownCount:               stats.DownCount,
		Total:                   stats.Total,
		WorstStatus:             string(stats.WorstStatus),
		LastCheckFormatted:      lastCheckStr,
		UptimeDurationFormatted: uptimeStr,
	}

	var buf bytes.Buffer
	if err := s.statsTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, "system not found", http.StatusNotFound)
		return
	}

	period, periodLabel, duration, bucketCount := parsePeriod(r.URL.Query().Get("period"), t)

	to := time.Now().UTC()
	from := to.Add(-duration)

	ctx := r.Context()
	var events []storage.StoredEvent
	var failed []storage.StoredCheck
	var buckets []storage.TimelineBucket
	stats := storage.SystemHistoryStats{DNSUptime: 100.0, IPUptime: 100.0}

	s.cfgMu.RLock()
	degLatMs := int64(2000)
	if s.cfg != nil && s.cfg.Defaults.DegradedLatency > 0 {
		degLatMs = s.cfg.Defaults.DegradedLatency.Milliseconds()
	}
	s.cfgMu.RUnlock()

	if s.storage != nil {
		var err error
		events, err = s.storage.ListEvents(ctx, id, from, to, 100)
		if err != nil {
			log.Printf("[WARN] list events: %v", err)
		}

		failed, err = s.storage.ListFailedChecks(ctx, id, from, to, 51, 0)
		if err != nil {
			log.Printf("[WARN] list failed checks: %v", err)
		}

		buckets, err = s.storage.GetTimelineBucketsWithLatency(ctx, id, from, to, bucketCount, degLatMs)
		if err != nil {
			log.Printf("[WARN] timeline buckets: %v", err)
		}

		stats, err = s.storage.GetSystemUptimeStatsWithEvents(ctx, id, from, to, events)
		if err != nil {
			log.Printf("[WARN] uptime stats: %v", err)
		}
	}

	eventVMs := make([]eventViewModel, len(events))
	var prevDate string
	for i, e := range events {
		loc := e.OccurredAt.Local()
		dateStr := loc.Format("02/01/2006")
		dateGroup := ""
		if dateStr != prevDate {
			dateGroup = dateStr
			prevDate = dateStr
		}
		eventVMs[i] = eventViewModel{
			ID:                  e.ID,
			Kind:                strings.ToUpper(e.Kind),
			FromStatus:          e.FromStatus,
			ToStatus:            e.ToStatus,
			ToStatusLower:       strings.ToLower(e.ToStatus),
			Diagnosis:           e.Diagnosis,
			Detail:              e.Detail,
			Note:                e.Note,
			PublicNote:          e.PublicNote,
			OccurredAtFormatted: loc.Format("02/01 15:04:05"),
			DateGroup:           dateGroup,
			DurationStr:         formatDurationShort(e.Duration),
		}
	}

	hasMoreFailed := len(failed) > 50
	if hasMoreFailed {
		failed = failed[:50]
	}

	failedVMs := make([]failedCheckViewModel, len(failed))
	for i, f := range failed {
		failedVMs[i] = failedCheckViewModel{
			Kind:               strings.ToUpper(f.Kind),
			StatusCode:         f.StatusCode,
			LatencyMs:          f.LatencyMs,
			ErrorCat:           f.ErrorCat,
			ErrorMsg:           f.ErrorMsg,
			CheckedAtFormatted: f.CheckedAt.Local().Format("02/01 15:04:05"),
		}
	}

	bucketVMs := make([]timelineBucketViewModel, len(buckets))
	for i, b := range buckets {
		dnsSt := b.DNSStatus
		ipSt := b.IPStatus
		if !b.HasData {
			dnsSt = "UNKNOWN"
			ipSt = "UNKNOWN"
		}
		timeFmt := b.StartTime.Local().Format("15:04")
		if period == "7d" || period == "30d" {
			timeFmt = b.StartTime.Local().Format("02/01 15:04")
		}
		dnsTip := fmt.Sprintf("%s: DNS %s", timeFmt, dnsSt)
		ipTip := fmt.Sprintf("%s: IP %s", timeFmt, ipSt)
		if b.HasData && b.TotalChecks > 0 {
			dnsTip = fmt.Sprintf("%s: DNS %s (%d/%d · %dms)", timeFmt, dnsSt, b.OKChecks, b.TotalChecks, b.AvgLatencyMs)
			ipTip = fmt.Sprintf("%s: IP %s (%d/%d · %dms)", timeFmt, ipSt, b.OKChecks, b.TotalChecks, b.AvgLatencyMs)
		}
		bucketVMs[i] = timelineBucketViewModel{
			DNSStatus:      dnsSt,
			DNSStatusLower: strings.ToLower(dnsSt),
			DNSTooltip:     dnsTip,
			IPStatus:       ipSt,
			IPStatusLower:  strings.ToLower(ipSt),
			IPTooltip:      ipTip,
			TimeFormatted:  timeFmt,
		}
	}

	latencyChart := buildLatencyChart(buckets, degLatMs, period)
	httpBreakdown := buildHTTPTraceBreakdown(stats)

	mttrStr := "—"
	if stats.MTTR > 0 {
		mttrStr = formatDurationShort(stats.MTTR)
	}
	mtbfStr := "—"
	if stats.MTBF > 0 {
		mtbfStr = formatDurationShort(stats.MTBF)
	}

	downDurStr := ""
	if snap.DownDuration > 0 {
		downDurStr = formatDurationShort(snap.DownDuration)
	}

	connSt := s.connectivityStatus()

	data := struct {
		T                    i18n.Messages
		Title                string
		IconsSVG             template.HTML
		HideSearch           bool
		HideNOCControls      bool
		SantaEasterEgg       bool
		SeasonalThemes       bool
		Offline              bool
		OfflineLastCheckText string
		Groups               []string
		System               monitor.SystemSnapshot
		WorstStatus          string
		WorstStatusLower     string
		Diagnosis            string
		DownDurationStr      string
		Period               string
		PeriodLabel          string
		Stats                storage.SystemHistoryStats
		MTTRStr              string
		MTBFStr              string
		Events               []eventViewModel
		FailedChecks         []failedCheckViewModel
		HasMoreFailed        bool
		Buckets              []timelineBucketViewModel
		LatencyChart         latencyChartViewModel
		HTTPBreakdown        httpTraceBreakdownViewModel
	}{
		T:                    t,
		Title:                fmt.Sprintf("%s — %s", snap.Name, t.HistorySuffix),
		IconsSVG:             s.iconsSVG,
		HideSearch:           true,
		HideNOCControls:      false,
		SantaEasterEgg:       s.santaEasterEggEnabled(),
		SeasonalThemes:       s.seasonalThemesEnabled(),
		Offline:              connSt.Offline,
		OfflineLastCheckText: formatOfflineLastCheck(connSt.LastChecked, t),
		System:               snap,
		WorstStatus:          string(snap.WorstStatus),
		WorstStatusLower:     strings.ToLower(string(snap.WorstStatus)),
		Diagnosis:            snap.Diagnosis,
		DownDurationStr:      downDurStr,
		Period:               period,
		PeriodLabel:          periodLabel,
		Stats:                stats,
		MTTRStr:              mttrStr,
		MTBFStr:              mtbfStr,
		Events:               eventVMs,
		FailedChecks:         failedVMs,
		HasMoreFailed:        hasMoreFailed,
		Buckets:              bucketVMs,
		LatencyChart:         latencyChart,
		HTTPBreakdown:        httpBreakdown,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.historyTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render history: %v", err)
	}
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	id := r.PathValue("id")
	snap, ok := s.store.GetSystem(id)
	if !ok {
		http.Error(w, "system not found", http.StatusNotFound)
		return
	}

	period, _, duration, _ := parsePeriod(r.URL.Query().Get("period"), t)
	to := time.Now().UTC()
	from := to.Add(-duration)
	ctx := r.Context()

	events := make([]storage.StoredEvent, 0)
	stats := storage.SystemHistoryStats{DNSUptime: 100.0, IPUptime: 100.0}
	if s.storage != nil {
		var err error
		events, err = s.storage.ListEvents(ctx, id, from, to, 1000)
		if err != nil {
			http.Error(w, "failed to load events", http.StatusInternalServerError)
			return
		}
		if events == nil {
			events = make([]storage.StoredEvent, 0)
		}
		stats, err = s.storage.GetSystemUptimeStatsWithEvents(ctx, id, from, to, events)
		if err != nil {
			http.Error(w, "failed to compute stats", http.StatusInternalServerError)
			return
		}
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"argus-%s-%s.csv\"", snap.ID, period))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"occurred_at", "kind", "from_status", "to_status", "diagnosis", "detail", "duration_seconds"})
		for _, ev := range events {
			_ = cw.Write([]string{
				ev.OccurredAt.Format(time.RFC3339),
				sanitizeCSVCell(ev.Kind),
				sanitizeCSVCell(ev.FromStatus),
				sanitizeCSVCell(ev.ToStatus),
				sanitizeCSVCell(ev.Diagnosis),
				sanitizeCSVCell(ev.Detail),
				fmt.Sprintf("%.0f", ev.Duration.Seconds()),
			})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			log.Printf("[WARN] csv export flush: %v", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"argus-%s-%s.json\"", snap.ID, period))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"system_id": snap.ID,
		"name":      snap.Name,
		"period":    period,
		"stats":     stats,
		"events":    events,
	})
}

func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	t := s.messagesForRequest(w, r)
	systems := s.store.GetAllSystems()
	stats := s.store.GetStats()

	overallLower := "up"
	overallLabel := t.AllSystemsOperational
	if stats.DownCount > 0 {
		overallLower = "down"
		overallLabel = fmt.Sprintf(t.SystemsDownCountFmt, stats.DownCount)
	} else if stats.DegradedCount > 0 {
		overallLower = "degraded"
		overallLabel = t.PartialDegradation
	}

	type publicDayVM struct {
		StatusLower string
		Tooltip     string
	}

	type publicSystemVM struct {
		Name        string
		Group       string
		StatusLower string
		StatusLabel string
		Uptime1h    float64
		Days        []publicDayVM
	}

	sysNames := make(map[string]string, len(systems))
	vms := make([]publicSystemVM, 0, len(systems))
	ctx := r.Context()

	for _, sys := range systems {
		sysNames[sys.ID] = sys.Name
		label := t.StatusNoData
		switch sys.WorstStatus {
		case monitor.StatusUP:
			label = t.StatusOperational
		case monitor.StatusDegraded:
			label = t.StatusDegraded
		case monitor.StatusDown:
			label = t.StatusDown
		}

		var daysVM []publicDayVM
		if s.storage != nil {
			if daily, err := s.storage.GetDailyUptimeBuckets(ctx, sys.ID, 30); err == nil {
				daysVM = make([]publicDayVM, len(daily))
				for i, d := range daily {
					stLower := strings.ToLower(d.Status)
					dateLabel := d.Day.UTC().Format("02/01")
					tip := fmt.Sprintf("%s: %s", dateLabel, t.StatusNoData)
					if d.HasData {
						tip = fmt.Sprintf("%s: %.1f%%", dateLabel, d.UptimePct)
					}
					daysVM[i] = publicDayVM{
						StatusLower: stLower,
						Tooltip:     tip,
					}
				}
			}
		}

		vms = append(vms, publicSystemVM{
			Name:        sys.Name,
			Group:       sys.Group,
			StatusLower: strings.ToLower(string(sys.WorstStatus)),
			StatusLabel: label,
			Uptime1h:    sys.Uptime1h,
			Days:        daysVM,
		})
	}

	type publicIncidentVM struct {
		SystemName          string
		ToStatus            string
		ToStatusLower       string
		Diagnosis           string
		PublicNote          string
		OccurredAtFormatted string
	}
	var recentIncidents []publicIncidentVM
	if s.storage != nil {
		if rawEvs, err := s.storage.ListAllRecentEvents(ctx, 8); err == nil {
			for _, ev := range rawEvs {
				name := sysNames[ev.SystemID]
				if name == "" {
					name = ev.SystemID
				}
				pubNote := ""
				if ev.PublicNote {
					pubNote = strings.TrimSpace(ev.Note)
				}
				recentIncidents = append(recentIncidents, publicIncidentVM{
					SystemName:          name,
					ToStatus:            translateStatus(ev.ToStatus, t),
					ToStatusLower:       strings.ToLower(ev.ToStatus),
					Diagnosis:           ev.Diagnosis,
					PublicNote:          pubNote,
					OccurredAtFormatted: ev.OccurredAt.Local().Format("02/01 15:04"),
				})
			}
		}
	}

	lastCheckStr := "—"
	if !stats.LastCheck.IsZero() {
		lastCheckStr = stats.LastCheck.Local().Format("02/01/2006 15:04:05")
	}

	connSt := s.connectivityStatus()

	data := struct {
		T                    i18n.Messages
		Title                string
		IconsSVG             template.HTML
		HideSearch           bool
		HideNOCControls      bool
		SantaEasterEgg       bool
		SeasonalThemes       bool
		Offline              bool
		OfflineLastCheckText string
		Groups               []string
		OverallStatusLower   string
		OverallLabel         string
		LastCheckFormatted   string
		Systems              []publicSystemVM
		RecentIncidents      []publicIncidentVM
	}{
		T:                    t,
		Title:                t.PublicStatusTitle,
		IconsSVG:             s.iconsSVG,
		HideSearch:           true,
		HideNOCControls:      true,
		SantaEasterEgg:       s.santaEasterEggEnabled(),
		SeasonalThemes:       s.seasonalThemesEnabled(),
		Offline:              connSt.Offline,
		OfflineLastCheckText: formatOfflineLastCheck(connSt.LastChecked, t),
		OverallStatusLower:   overallLower,
		OverallLabel:         overallLabel,
		LastCheckFormatted:   lastCheckStr,
		Systems:              vms,
		RecentIncidents:      recentIncidents,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.statusTmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] render status: %v", err)
	}
}

func sanitizeCSVCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

func translateStatus(status string, t i18n.Messages) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "UP":
		return t.StatusOperational
	case "DEGRADED":
		return t.StatusDegraded
	case "DOWN":
		return t.StatusDown
	default:
		return status
	}
}
