package monitor

import (
	"context"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
)

// Scheduler coordinates timed background execution of checks.
type Scheduler struct {
	cfg                  *config.Config
	store                *Store
	httpChecker          *checker.HTTPChecker
	pingChecker          *checker.PingChecker
	wg                   sync.WaitGroup
	mu                   sync.RWMutex
	ctx                  context.Context
	cancel               context.CancelFunc
	probeMu              sync.Mutex
	probeFn              func(context.Context) bool
	probeInterval        time.Duration
	connectivity         ConnectivityStatus
	onConnectivityChange func(ConnectivityStatus)
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	cfg *config.Config,
	store *Store,
	httpChecker *checker.HTTPChecker,
	pingChecker *checker.PingChecker,
) *Scheduler {
	return &Scheduler{
		cfg:           cfg,
		store:         store,
		httpChecker:   httpChecker,
		pingChecker:   pingChecker,
		probeFn:       defaultConnectivityProbe,
		probeInterval: defaultProbeInterval,
	}
}

// SetConnectivityProbe overrides the internet connectivity probe (intended for testing).
func (s *Scheduler) SetConnectivityProbe(fn func(context.Context) bool) {
	s.mu.Lock()
	if fn != nil {
		s.probeFn = fn
	}
	s.mu.Unlock()
}

// SetOnConnectivityChange registers a callback invoked when connectivity status updates.
func (s *Scheduler) SetOnConnectivityChange(fn func(ConnectivityStatus)) {
	s.mu.Lock()
	s.onConnectivityChange = fn
	s.mu.Unlock()
}

// ConnectivityStatus returns the current internet connectivity state.
func (s *Scheduler) ConnectivityStatus() ConnectivityStatus {
	s.mu.RLock()
	st := s.connectivity
	s.mu.RUnlock()
	return st
}

// IsOffline reports whether monitoring is currently paused due to lost internet connectivity.
func (s *Scheduler) IsOffline() bool {
	s.mu.RLock()
	offline := s.connectivity.Offline
	s.mu.RUnlock()
	return offline
}

// Start launches monitoring goroutines for each configured system check.
func (s *Scheduler) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	s.mu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	s.mu.Unlock()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	s.wg.Add(1)
	go s.runConnectivityLoop(ctx)

	for _, sys := range s.cfg.Systems {
		sys := sys
		interval := sys.EffectiveInterval(s.cfg.Defaults)

		if strings.TrimSpace(sys.URL) != "" {
			jitter := time.Duration(r.Int63n(int64(interval / 3)))
			s.wg.Add(1)
			go s.runLoop(ctx, jitter, interval, func(c context.Context) {
				s.executeDNS(c, sys)
			})
		}

		if strings.TrimSpace(sys.IP) != "" {
			jitter := time.Duration(r.Int63n(int64(interval / 3)))
			s.wg.Add(1)
			go s.runLoop(ctx, jitter, interval, func(c context.Context) {
				s.executeIP(c, sys)
			})
		}
	}
}

// Stop cancels monitoring and waits for all active goroutines to finish.
func (s *Scheduler) Stop() {
	s.mu.RLock()
	cancel := s.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

// CheckNow triggers an immediate asynchronous check for the specified system ID.
func (s *Scheduler) CheckNow(sysID string) bool {
	if s.IsOffline() {
		return false
	}
	s.mu.RLock()
	baseCtx := s.ctx
	var target *config.SystemConfig
	for i := range s.cfg.Systems {
		if s.cfg.Systems[i].ID == sysID {
			sysCopy := s.cfg.Systems[i]
			target = &sysCopy
			break
		}
	}
	s.mu.RUnlock()

	if target == nil {
		return false
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	s.wg.Add(1)
	go func(sys config.SystemConfig) {
		defer s.wg.Done()
		checkTimeout := 15 * time.Second
		if sys.HTTPTimeout > checkTimeout {
			checkTimeout = sys.HTTPTimeout + 2*time.Second
		}
		if sys.PingTimeout > checkTimeout {
			checkTimeout = sys.PingTimeout + 2*time.Second
		}
		ctx, cancel := context.WithTimeout(baseCtx, checkTimeout)
		defer cancel()

		var checkWg sync.WaitGroup
		if strings.TrimSpace(sys.URL) != "" {
			checkWg.Add(1)
			go func() {
				defer checkWg.Done()
				s.executeDNS(ctx, sys)
			}()
		}
		if strings.TrimSpace(sys.IP) != "" {
			checkWg.Add(1)
			go func() {
				defer checkWg.Done()
				s.executeIP(ctx, sys)
			}()
		}
		checkWg.Wait()
	}(*target)

	return true
}

func (s *Scheduler) runLoop(ctx context.Context, jitter, interval time.Duration, fn func(context.Context)) {
	defer s.wg.Done()

	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	fn(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

func (s *Scheduler) httpOptions(sys config.SystemConfig, targetURL string, expectedDNS []string) checker.CheckOptions {
	return checker.CheckOptions{
		URL:                targetURL,
		Method:             sys.Method,
		Headers:            sys.Headers,
		HealthPath:         sys.HealthPath,
		ExpectedStatus:     sys.ExpectedStatus,
		ExpectedBody:       sys.ExpectedBody,
		ExpectedBodyRegex:  sys.BodyRegex(),
		ExpectedDNS:        expectedDNS,
		JSONPath:           sys.JSONPath,
		Timeout:            sys.EffectiveHTTPTimeout(s.cfg.Defaults),
		InsecureSkipVerify: sys.InsecureSkipVerify,
	}
}

func (s *Scheduler) executeDNS(ctx context.Context, sys config.SystemConfig) {
	if s.IsOffline() {
		return
	}
	res := s.httpChecker.Check(ctx, s.httpOptions(sys, sys.URL, sys.ExpectedDNS))
	if !res.OK && s.verifyOfflineOnFailure(ctx) {
		return
	}
	s.store.RecordDNSResult(sys.ID, res)
}

func (s *Scheduler) executeIP(ctx context.Context, sys config.SystemConfig) {
	if s.IsOffline() {
		return
	}
	if sys.IsIPHTTP() {
		res := s.httpChecker.Check(ctx, s.httpOptions(sys, sys.IP, nil))
		if !res.OK && s.verifyOfflineOnFailure(ctx) {
			return
		}
		s.store.RecordIPResult(sys.ID, res)
		return
	}

	timeout := sys.EffectivePingTimeout(s.cfg.Defaults)

	var res checker.Result
	if sys.TCPPort > 0 {
		res = s.pingChecker.CheckTCP(ctx, sys.IP, sys.TCPPort, timeout)
	} else {
		res = s.pingChecker.CheckICMP(ctx, sys.IP, timeout)
		if !res.OK && res.ErrorCat == checker.CatPermission {
			if tcpRes, ok := s.fallbackTCP(ctx, sys.IP, timeout); ok {
				res = tcpRes
			}
		}
	}

	if !res.OK && s.verifyOfflineOnFailure(ctx) {
		return
	}
	s.store.RecordIPResult(sys.ID, res)
}

func (s *Scheduler) runConnectivityLoop(ctx context.Context) {
	defer s.wg.Done()

	s.mu.RLock()
	interval := s.probeInterval
	s.mu.RUnlock()
	if interval <= 0 {
		interval = defaultProbeInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.probeAndUpdate(ctx, false)
		}
	}
}

func (s *Scheduler) verifyOfflineOnFailure(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	return s.probeAndUpdate(ctx, true)
}

func (s *Scheduler) probeAndUpdate(ctx context.Context, coalesce bool) bool {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()

	if ctx.Err() != nil {
		return s.IsOffline()
	}

	s.mu.RLock()
	cur := s.connectivity
	probe := s.probeFn
	s.mu.RUnlock()

	if coalesce && cur.Offline {
		return true
	}
	if coalesce && !cur.LastChecked.IsZero() && time.Since(cur.LastChecked) < probeCoalesceWindow {
		return cur.Offline
	}
	if probe == nil {
		return false
	}

	online := probe(ctx)
	if ctx.Err() != nil {
		return s.IsOffline()
	}

	now := time.Now()
	var cb func(ConnectivityStatus)
	var next ConnectivityStatus
	notify := false

	s.mu.Lock()
	prev := s.connectivity
	next = ConnectivityStatus{
		Offline:      !online,
		LastChecked:  now,
		Transitioned: !online != prev.Offline,
	}
	if next.Offline {
		if prev.Offline && !prev.Since.IsZero() {
			next.Since = prev.Since
		} else {
			next.Since = now
		}
		notify = true
	} else if prev.Offline {
		next.RecoveredFrom = prev.Since
		notify = true
	}
	s.connectivity = next
	cb = s.onConnectivityChange
	s.mu.Unlock()

	if next.Offline && !prev.Offline {
		slog.Warn("conexão com a internet perdida; monitoramento pausado")
	} else if !next.Offline && prev.Offline {
		slog.Info("conexão com a internet restabelecida; monitoramento retomado")
	}

	if notify && cb != nil {
		cb(next)
	}
	return next.Offline
}

func (s *Scheduler) fallbackTCP(ctx context.Context, ip string, timeout time.Duration) (checker.Result, bool) {
	ports := []int{80, 443, 22}
	fbCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resCh := make(chan checker.Result, len(ports))
	var wg sync.WaitGroup
	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			r := s.pingChecker.CheckTCP(fbCtx, ip, p, timeout)
			if r.OK {
				resCh <- r
			}
		}(port)
	}

	go func() {
		wg.Wait()
		close(resCh)
	}()

	var firstResult checker.Result
	var found bool
	for r := range resCh {
		if !found {
			firstResult = r
			found = true
			cancel()
		}
	}
	return firstResult, found
}
