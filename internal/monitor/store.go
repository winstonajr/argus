package monitor

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
	"argus/internal/i18n"
)

const defaultRingBufferSize = 120

func ringSizeForInterval(interval time.Duration) int {
	if interval <= 0 {
		return defaultRingBufferSize
	}
	needed := int(time.Hour/interval) + 2
	if needed > defaultRingBufferSize {
		return needed
	}
	return defaultRingBufferSize
}

type checkState struct {
	configured      bool
	status          Status
	lastResult      checker.Result
	lastChange      time.Time
	consecFails     int
	history         []checker.Result
	historyHead     int
	historyCount    int
	failThreshold   int
	degradedLatency time.Duration
	tlsWarnDays     int
}

func newCheckState(configured bool, failThreshold int, degradedLatency time.Duration, tlsWarnDays int, ringSize int) *checkState {
	if ringSize < defaultRingBufferSize {
		ringSize = defaultRingBufferSize
	}
	return &checkState{
		configured:      configured,
		status:          StatusUnknown,
		failThreshold:   failThreshold,
		degradedLatency: degradedLatency,
		tlsWarnDays:     tlsWarnDays,
		history:         make([]checker.Result, ringSize),
	}
}

func (cs *checkState) pushResult(res checker.Result) {
	if !cs.configured || len(cs.history) == 0 {
		return
	}
	capSize := len(cs.history)
	cs.history[cs.historyHead] = res
	cs.historyHead = (cs.historyHead + 1) % capSize
	if cs.historyCount < capSize {
		cs.historyCount++
	}
	cs.lastResult = res
}

func (cs *checkState) uptime(now time.Time) float64 {
	if cs.historyCount == 0 {
		return 100.0
	}
	cutoff := now.Add(-1 * time.Hour)
	windowCount := 0
	okCount := 0
	for i := 0; i < cs.historyCount; i++ {
		res := cs.history[i]
		if res.CheckedAt.IsZero() || res.CheckedAt.Before(cutoff) {
			continue
		}
		windowCount++
		if res.OK {
			okCount++
		}
	}
	if windowCount == 0 {
		return 100.0
	}
	return (float64(okCount) / float64(windowCount)) * 100.0
}

func (cs *checkState) snapshot(now time.Time) CheckSnapshot {
	return CheckSnapshot{
		Configured:  cs.configured,
		Status:      cs.status,
		LastResult:  cs.lastResult,
		LastChange:  cs.lastChange,
		ConsecFails: cs.consecFails,
		Uptime1h:    cs.uptime(now),
		HasHistory:  cs.historyCount > 0,
		TLSWarnDays: cs.tlsWarnDays,
	}
}

type systemState struct {
	cfg              config.SystemConfig
	interval         time.Duration
	dns              *checkState
	ip               *checkState
	inMaintenance    bool
	maintenanceUntil time.Time
	lastRun          time.Time
}

func (sys *systemState) isInMaintenance(now time.Time) bool {
	if !sys.inMaintenance {
		return false
	}
	if !sys.maintenanceUntil.IsZero() && !now.Before(sys.maintenanceUntil) {
		return false
	}
	return true
}

// Store maintains the live in-memory state of all systems.
type Store struct {
	mu               sync.RWMutex
	systems          map[string]*systemState
	order            []string
	startTime        time.Time
	lastCheckTime    time.Time
	eventSubscribers []func(ChangeEvent)
	checkSubscribers []func(sysID string, kind string, res checker.Result)
}

// NewStore initializes a new Store with systems from configuration.
func NewStore(cfg *config.Config) *Store {
	s := &Store{
		systems:   make(map[string]*systemState),
		order:     make([]string, 0, len(cfg.Systems)),
		startTime: time.Now(),
	}

	for _, sys := range cfg.Systems {
		failThresh := sys.EffectiveFailThreshold(cfg.Defaults)
		degLat := cfg.Defaults.DegradedLatency
		if degLat <= 0 {
			degLat = 2 * time.Second
		}

		hasDNS := strings.TrimSpace(sys.URL) != ""
		hasIP := strings.TrimSpace(sys.IP) != ""
		tlsWarnDays := sys.EffectiveTLSWarnDays(cfg.Defaults)
		interval := sys.EffectiveInterval(cfg.Defaults)
		ringSize := ringSizeForInterval(interval)

		s.systems[sys.ID] = &systemState{
			cfg:           sys,
			interval:      interval,
			dns:           newCheckState(hasDNS, failThresh, degLat, tlsWarnDays, ringSize),
			ip:            newCheckState(hasIP, failThresh, degLat, tlsWarnDays, ringSize),
			inMaintenance: sys.Maintenance,
			lastRun:       time.Time{},
		}
		s.order = append(s.order, sys.ID)
	}

	return s
}

// SetMaintenance toggles maintenance mode for a system. Returns false if system not found.
func (s *Store) SetMaintenance(sysID string, enabled bool) bool {
	return s.SetMaintenanceUntil(sysID, enabled, time.Time{})
}

// SetMaintenanceUntil toggles maintenance mode with an optional expiration timestamp.
func (s *Store) SetMaintenanceUntil(sysID string, enabled bool, until time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sys, ok := s.systems[sysID]
	if !ok {
		return false
	}
	sys.inMaintenance = enabled
	if enabled {
		sys.maintenanceUntil = until
	} else {
		sys.maintenanceUntil = time.Time{}
	}
	return true
}

// ReloadConfig updates systems from a new config while preserving in-memory check history for retained systems.
func (s *Store) ReloadConfig(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newSystems := make(map[string]*systemState, len(cfg.Systems))
	newOrder := make([]string, 0, len(cfg.Systems))

	for _, sys := range cfg.Systems {
		failThresh := sys.EffectiveFailThreshold(cfg.Defaults)
		degLat := cfg.Defaults.DegradedLatency
		if degLat <= 0 {
			degLat = 2 * time.Second
		}
		hasDNS := strings.TrimSpace(sys.URL) != ""
		hasIP := strings.TrimSpace(sys.IP) != ""
		tlsWarnDays := sys.EffectiveTLSWarnDays(cfg.Defaults)
		interval := sys.EffectiveInterval(cfg.Defaults)
		ringSize := ringSizeForInterval(interval)

		if existing, ok := s.systems[sys.ID]; ok {
			existing.cfg = sys
			existing.interval = interval
			existing.inMaintenance = sys.Maintenance
			existing.maintenanceUntil = time.Time{}
			existing.dns.configured = hasDNS
			existing.dns.failThreshold = failThresh
			existing.dns.degradedLatency = degLat
			existing.dns.tlsWarnDays = tlsWarnDays
			existing.ip.configured = hasIP
			existing.ip.failThreshold = failThresh
			existing.ip.degradedLatency = degLat
			existing.ip.tlsWarnDays = tlsWarnDays
			newSystems[sys.ID] = existing
		} else {
			newSystems[sys.ID] = &systemState{
				cfg:           sys,
				interval:      interval,
				dns:           newCheckState(hasDNS, failThresh, degLat, tlsWarnDays, ringSize),
				ip:            newCheckState(hasIP, failThresh, degLat, tlsWarnDays, ringSize),
				inMaintenance: sys.Maintenance,
			}
		}
		newOrder = append(newOrder, sys.ID)
	}

	s.systems = newSystems
	s.order = newOrder
}

// SubscribeEvents registers a callback for status change events.
func (s *Store) SubscribeEvents(fn func(ChangeEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventSubscribers = append(s.eventSubscribers, fn)
}

// SubscribeChecks registers a callback for every individual check completed.
func (s *Store) SubscribeChecks(fn func(sysID string, kind string, res checker.Result)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkSubscribers = append(s.checkSubscribers, fn)
}

// RecordDNSResult saves DNS/HTTP check results and evaluates status transitions.
func (s *Store) RecordDNSResult(sysID string, res checker.Result) {
	s.recordResult(sysID, "dns", res)
}

// RecordIPResult saves ICMP/TCP check results and evaluates status transitions.
func (s *Store) RecordIPResult(sysID string, res checker.Result) {
	s.recordResult(sysID, "ip", res)
}

func (cs *checkState) evaluateNextStatus(res checker.Result) Status {
	if res.OK {
		cs.consecFails = 0
		if res.HasTLS && res.TLSDaysLeft < 0 {
			return StatusDown
		}
		if (cs.degradedLatency > 0 && res.Latency > cs.degradedLatency) ||
			res.Loss > 0 ||
			(res.HasTLS && cs.tlsWarnDays > 0 && res.TLSDaysLeft < cs.tlsWarnDays) {
			return StatusDegraded
		}
		return StatusUP
	}
	cs.consecFails++
	if cs.consecFails >= cs.failThreshold {
		return StatusDown
	}
	return StatusDegraded
}

// HydrateCheck pre-populates a check's ring buffer and status from historical results
// (ordered oldest to newest) without triggering event or check subscribers.
func (s *Store) HydrateCheck(sysID string, kind string, results []checker.Result, lastChange time.Time) {
	if len(results) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sys, ok := s.systems[sysID]
	if !ok {
		return
	}

	target := sys.ip
	if kind == "dns" {
		target = sys.dns
	}
	if target == nil || !target.configured {
		return
	}

	for _, res := range results {
		target.pushResult(res)
		target.status = target.evaluateNextStatus(res)
		if res.CheckedAt.After(s.lastCheckTime) {
			s.lastCheckTime = res.CheckedAt
		}
		if res.CheckedAt.After(sys.lastRun) {
			sys.lastRun = res.CheckedAt
		}
	}

	if !lastChange.IsZero() {
		target.lastChange = lastChange
	} else if !target.lastResult.CheckedAt.IsZero() {
		target.lastChange = target.lastResult.CheckedAt
	}
}

func (s *Store) recordResult(sysID string, kind string, res checker.Result) {
	var event *ChangeEvent
	var checkSubs []func(string, string, checker.Result)
	var eventSubs []func(ChangeEvent)

	s.mu.Lock()
	sys, ok := s.systems[sysID]
	if !ok {
		s.mu.Unlock()
		return
	}

	s.lastCheckTime = res.CheckedAt
	sys.lastRun = res.CheckedAt

	var target *checkState
	if kind == "dns" {
		target = sys.dns
	} else {
		target = sys.ip
	}

	if target == nil || !target.configured {
		s.mu.Unlock()
		return
	}

	target.pushResult(res)
	oldStatus := target.status
	newStatus := target.evaluateNextStatus(res)

	if newStatus != oldStatus {
		target.status = newStatus
		target.lastChange = res.CheckedAt

		now := time.Now()
		dnsSnap := sys.dns.snapshot(now)
		ipSnap := sys.ip.snapshot(now)
		summary, detail := Diagnose(&dnsSnap, &ipSnap)

		event = &ChangeEvent{
			SystemID:      sys.cfg.ID,
			SystemName:    sys.cfg.Name,
			Kind:          kind,
			FromStatus:    oldStatus,
			ToStatus:      newStatus,
			Diagnosis:     summary,
			Detail:        detail,
			InMaintenance: sys.isInMaintenance(now),
			OccurredAt:    res.CheckedAt,
		}
	}

	checkSubs = append(checkSubs, s.checkSubscribers...)
	if event != nil {
		eventSubs = append(eventSubs, s.eventSubscribers...)
	}
	s.mu.Unlock()

	// Dispatch callbacks outside lock to prevent deadlocks
	for _, sub := range checkSubs {
		sub(sysID, kind, res)
	}
	if event != nil {
		for _, sub := range eventSubs {
			sub(*event)
		}
	}
}

// GetSystem returns a snapshot of a single system.
func (s *Store) GetSystem(id string) (SystemSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sys, ok := s.systems[id]
	if !ok {
		return SystemSnapshot{}, false
	}
	return s.buildSnapshot(sys), true
}

// GetAllSystems returns all systems ordered by severity (DOWN > DEGRADED > UNKNOWN > UP).
func (s *Store) GetAllSystems() []SystemSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]SystemSnapshot, 0, len(s.order))
	for _, id := range s.order {
		if sys, ok := s.systems[id]; ok {
			res = append(res, s.buildSnapshot(sys))
		}
	}

	// Order: DOWN first, then DEGRADED, then UNKNOWN, then UP; alphabetical by name
	sort.SliceStable(res, func(i, j int) bool {
		sevI := res[i].WorstStatus.Severity()
		sevJ := res[j].WorstStatus.Severity()
		if sevI != sevJ {
			return sevI > sevJ
		}
		return res[i].Name < res[j].Name
	})

	return res
}

// GetSummaryCounts returns lightweight system-wide aggregates without building full snapshots.
func (s *Store) GetSummaryCounts() StatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := StatsSnapshot{
		Total:       len(s.systems),
		WorstStatus: StatusUP,
		LastCheck:   s.lastCheckTime,
		UptimeStart: s.startTime,
	}

	for _, sys := range s.systems {
		worst := StatusUnknown
		if sys.dns.configured && sys.ip.configured {
			worst = WorstStatus(sys.dns.status, sys.ip.status)
		} else if sys.dns.configured {
			worst = sys.dns.status
		} else if sys.ip.configured {
			worst = sys.ip.status
		}

		switch worst {
		case StatusUP:
			stats.UpCount++
		case StatusDegraded:
			stats.DegradedCount++
		case StatusDown:
			stats.DownCount++
		case StatusUnknown:
			stats.UnknownCount++
		}
		stats.WorstStatus = WorstStatus(stats.WorstStatus, worst)
	}

	if stats.Total > 0 {
		stats.UptimeRatio = float64(stats.UpCount) / float64(stats.Total) * 100.0
	} else {
		stats.UptimeRatio = 100.0
	}

	return stats
}

// GetStats returns current system-wide aggregates.
func (s *Store) GetStats() StatsSnapshot {
	return s.GetSummaryCounts()
}

func (s *Store) buildSnapshot(sys *systemState) SystemSnapshot {
	now := time.Now()
	dnsSnap := sys.dns.snapshot(now)
	ipSnap := sys.ip.snapshot(now)

	worst := StatusUnknown
	uptime1h := 100.0
	if dnsSnap.Configured && ipSnap.Configured {
		worst = WorstStatus(dnsSnap.Status, ipSnap.Status)
		uptime1h = math.Min(dnsSnap.Uptime1h, ipSnap.Uptime1h)
	} else if dnsSnap.Configured {
		worst = dnsSnap.Status
		uptime1h = dnsSnap.Uptime1h
	} else if ipSnap.Configured {
		worst = ipSnap.Status
		uptime1h = ipSnap.Uptime1h
	}

	diagSummary, _ := Diagnose(&dnsSnap, &ipSnap)

	var lastChecked time.Time
	if !dnsSnap.LastResult.CheckedAt.IsZero() {
		lastChecked = dnsSnap.LastResult.CheckedAt
	}
	if !ipSnap.LastResult.CheckedAt.IsZero() && ipSnap.LastResult.CheckedAt.After(lastChecked) {
		lastChecked = ipSnap.LastResult.CheckedAt
	}

	var lastChange time.Time
	if !dnsSnap.LastChange.IsZero() {
		lastChange = dnsSnap.LastChange
	}
	if !ipSnap.LastChange.IsZero() && ipSnap.LastChange.After(lastChange) {
		lastChange = ipSnap.LastChange
	}

	var downDuration time.Duration
	if worst == StatusDown && !lastChange.IsZero() {
		downDuration = time.Since(lastChange)
	}

	// JustChanged: status changed within last 30s
	justChanged := !lastChange.IsZero() && time.Since(lastChange) < 30*time.Second

	// Heartbeat: construct up to 30 recent ticks
	heartbeat := buildHeartbeat(sys)

	// Latency history for sparkline
	latencies := buildLatencyHistory(sys)

	return SystemSnapshot{
		ID:             sys.cfg.ID,
		Name:           sys.cfg.Name,
		Group:          sys.cfg.Group,
		Tags:           sys.cfg.Tags,
		URL:            sys.cfg.URL,
		IP:             sys.cfg.IP,
		IsIPHTTP:       sys.cfg.IsIPHTTP(),
		TCPPort:        sys.cfg.TCPPort,
		Interval:       sys.interval,
		WorstStatus:    worst,
		Diagnosis:      diagSummary,
		InMaintenance:  sys.isInMaintenance(now),
		DownDuration:   downDuration,
		Uptime1h:       uptime1h,
		LastCheckedAt:  lastChecked,
		LastChange:     lastChange,
		DNS:            dnsSnap,
		IPCheck:        ipSnap,
		Heartbeat:      heartbeat,
		LatencyHistory: latencies,
		JustChanged:    justChanged,
	}
}

func buildHeartbeat(sys *systemState) []HeartbeatTick {
	const count = 30
	ticks := make([]HeartbeatTick, count)

	// Collect chronological samples from history
	totalSamples := sys.dns.historyCount
	if sys.ip.historyCount > totalSamples {
		totalSamples = sys.ip.historyCount
	}

	for i := 0; i < count; i++ {
		ticks[i] = HeartbeatTick{
			Status:  StatusUnknown,
			Tooltip: i18n.Pick("Sem dados", "No data"),
		}
	}

	if totalSamples == 0 {
		return ticks
	}

	limit := totalSamples
	if limit > count {
		limit = count
	}

	// Fill right-aligned (most recent on right)
	for i := 0; i < limit; i++ {
		slotIdx := count - limit + i
		// Read backwards from head
		dnsIdx := getHistoryIndex(sys.dns, limit-1-i)
		ipIdx := getHistoryIndex(sys.ip, limit-1-i)

		var dnsRes *checker.Result
		var ipRes *checker.Result
		if dnsIdx >= 0 && sys.dns.configured {
			dnsRes = &sys.dns.history[dnsIdx]
		}
		if ipIdx >= 0 && sys.ip.configured {
			ipRes = &sys.ip.history[ipIdx]
		}

		st := StatusUP
		var tooltip string
		var latency time.Duration

		if dnsRes != nil {
			latency = dnsRes.Latency
			if !dnsRes.OK {
				st = StatusDown
				tooltip = dnsRes.Error
			} else if sys.dns.degradedLatency > 0 && dnsRes.Latency > sys.dns.degradedLatency {
				st = StatusDegraded
			}
		}
		if ipRes != nil {
			if ipRes.Latency > latency {
				latency = ipRes.Latency
			}
			if !ipRes.OK {
				st = StatusDown
				if tooltip == "" {
					tooltip = ipRes.Error
				}
			} else if st != StatusDown && (ipRes.Loss > 0 || (sys.ip.degradedLatency > 0 && ipRes.Latency > sys.ip.degradedLatency)) {
				st = StatusDegraded
			}
		}

		switch st {
		case StatusUP:
			tooltip = i18n.Pickf("Operacional (%dms)", "Operational (%dms)", latency.Milliseconds())
		case StatusDegraded:
			tooltip = i18n.Pickf("Instável (%dms)", "Degraded (%dms)", latency.Milliseconds())
		case StatusDown:
			if tooltip == "" {
				tooltip = i18n.Pick("Fora do ar", "Down")
			}
		}

		ticks[slotIdx] = HeartbeatTick{
			Status:  st,
			Latency: latency,
			Tooltip: tooltip,
		}
	}

	return ticks
}

func getHistoryIndex(cs *checkState, offsetFromLatest int) int {
	capSize := len(cs.history)
	if !cs.configured || cs.historyCount == 0 || capSize == 0 || offsetFromLatest >= cs.historyCount {
		return -1
	}
	idx := cs.historyHead - 1 - offsetFromLatest
	for idx < 0 {
		idx += capSize
	}
	return idx % capSize
}

func buildLatencyHistory(sys *systemState) []int64 {
	const maxPoints = 15
	target := sys.dns
	if !target.configured {
		target = sys.ip
	}
	if target == nil || target.historyCount == 0 {
		return nil
	}

	n := target.historyCount
	if n > maxPoints {
		n = maxPoints
	}

	res := make([]int64, n)
	for i := 0; i < n; i++ {
		idx := getHistoryIndex(target, n-1-i)
		if idx >= 0 {
			res[i] = target.history[idx].Latency.Milliseconds()
		}
	}
	return res
}
