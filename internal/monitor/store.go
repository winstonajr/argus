package monitor

import (
	"sort"
	"strings"
	"sync"
	"time"

	"sentinela/internal/checker"
	"sentinela/internal/config"
)

const ringBufferSize = 120

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
}

func newCheckState(configured bool, failThreshold int, degradedLatency time.Duration) *checkState {
	return &checkState{
		configured:      configured,
		status:          StatusUnknown,
		failThreshold:   failThreshold,
		degradedLatency: degradedLatency,
		history:         make([]checker.Result, ringBufferSize),
	}
}

func (cs *checkState) pushResult(res checker.Result) {
	if !cs.configured {
		return
	}
	cs.history[cs.historyHead] = res
	cs.historyHead = (cs.historyHead + 1) % ringBufferSize
	if cs.historyCount < ringBufferSize {
		cs.historyCount++
	}
	cs.lastResult = res
}

func (cs *checkState) uptime() float64 {
	if cs.historyCount == 0 {
		return 100.0
	}
	okCount := 0
	for i := 0; i < cs.historyCount; i++ {
		if cs.history[i].OK {
			okCount++
		}
	}
	return (float64(okCount) / float64(cs.historyCount)) * 100.0
}

func (cs *checkState) snapshot() CheckSnapshot {
	return CheckSnapshot{
		Configured:  cs.configured,
		Status:      cs.status,
		LastResult:  cs.lastResult,
		LastChange:  cs.lastChange,
		ConsecFails: cs.consecFails,
		Uptime1h:    cs.uptime(),
		HasHistory:  cs.historyCount > 0,
	}
}

type systemState struct {
	cfg     config.SystemConfig
	dns     *checkState
	ip      *checkState
	lastRun time.Time
}

// Store maintains the live in-memory state of all systems.
type Store struct {
	mu              sync.RWMutex
	systems         map[string]*systemState
	order           []string
	startTime       time.Time
	lastCheckTime   time.Time
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

		s.systems[sys.ID] = &systemState{
			cfg:     sys,
			dns:     newCheckState(hasDNS, failThresh, degLat),
			ip:      newCheckState(hasIP, failThresh, degLat),
			lastRun: time.Time{},
		}
		s.order = append(s.order, sys.ID)
	}

	return s
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
	newStatus := oldStatus

	if res.OK {
		target.consecFails = 0
		if (target.degradedLatency > 0 && res.Latency > target.degradedLatency) || res.Loss > 0 {
			newStatus = StatusDegraded
		} else {
			newStatus = StatusUP
		}
	} else {
		target.consecFails++
		if target.consecFails >= target.failThreshold {
			newStatus = StatusDown
		} else {
			newStatus = StatusDegraded
		}
	}

	if newStatus != oldStatus {
		target.status = newStatus
		target.lastChange = res.CheckedAt

		dnsSnap := sys.dns.snapshot()
		ipSnap := sys.ip.snapshot()
		summary, detail := Diagnose(&dnsSnap, &ipSnap)

		event = &ChangeEvent{
			SystemID:   sys.cfg.ID,
			SystemName: sys.cfg.Name,
			Kind:       kind,
			FromStatus: oldStatus,
			ToStatus:   newStatus,
			Diagnosis:  summary,
			Detail:     detail,
			OccurredAt: res.CheckedAt,
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

// GetStats returns current system-wide aggregates.
func (s *Store) GetStats() StatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := StatsSnapshot{
		Total:       len(s.systems),
		WorstStatus: StatusUP,
		LastCheck:   s.lastCheckTime,
		UptimeStart: s.startTime,
	}

	for _, sys := range s.systems {
		snap := s.buildSnapshot(sys)
		switch snap.WorstStatus {
		case StatusUP:
			stats.UpCount++
		case StatusDegraded:
			stats.DegradedCount++
		case StatusDown:
			stats.DownCount++
		case StatusUnknown:
			stats.UnknownCount++
		}
		stats.WorstStatus = WorstStatus(stats.WorstStatus, snap.WorstStatus)
	}

	if stats.Total > 0 {
		stats.UptimeRatio = float64(stats.UpCount) / float64(stats.Total) * 100.0
	} else {
		stats.UptimeRatio = 100.0
	}

	return stats
}

func (s *Store) buildSnapshot(sys *systemState) SystemSnapshot {
	dnsSnap := sys.dns.snapshot()
	ipSnap := sys.ip.snapshot()

	worst := StatusUnknown
	if dnsSnap.Configured && ipSnap.Configured {
		worst = WorstStatus(dnsSnap.Status, ipSnap.Status)
	} else if dnsSnap.Configured {
		worst = dnsSnap.Status
	} else if ipSnap.Configured {
		worst = ipSnap.Status
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
		URL:            sys.cfg.URL,
		IP:             sys.cfg.IP,
		IsIPHTTP:       sys.cfg.IsIPHTTP(),
		TCPPort:        sys.cfg.TCPPort,
		WorstStatus:    worst,
		Diagnosis:      diagSummary,
		DownDuration:   downDuration,
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
			Tooltip: "Sem dados",
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
		tooltip := "Operacional"
		var latency time.Duration

		if dnsRes != nil {
			latency = dnsRes.Latency
			if !dnsRes.OK {
				st = StatusDown
				tooltip = dnsRes.Error
			}
		}
		if ipRes != nil {
			if ipRes.Latency > latency {
				latency = ipRes.Latency
			}
			if !ipRes.OK {
				st = StatusDown
				tooltip = ipRes.Error
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
	if !cs.configured || cs.historyCount == 0 || offsetFromLatest >= cs.historyCount {
		return -1
	}
	idx := cs.historyHead - 1 - offsetFromLatest
	for idx < 0 {
		idx += ringBufferSize
	}
	return idx % ringBufferSize
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
