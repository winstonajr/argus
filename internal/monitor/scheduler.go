package monitor

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"time"

	"argus/internal/checker"
	"argus/internal/config"
)

// Scheduler coordinates timed background execution of checks.
type Scheduler struct {
	cfg         *config.Config
	store       *Store
	httpChecker *checker.HTTPChecker
	pingChecker *checker.PingChecker
	wg          sync.WaitGroup
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	cfg *config.Config,
	store *Store,
	httpChecker *checker.HTTPChecker,
	pingChecker *checker.PingChecker,
) *Scheduler {
	return &Scheduler{
		cfg:         cfg,
		store:       store,
		httpChecker: httpChecker,
		pingChecker: pingChecker,
	}
}

// Start launches monitoring goroutines for each configured system check.
func (s *Scheduler) Start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	s.mu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	s.mu.Unlock()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	for _, sys := range s.cfg.Systems {
		interval := sys.EffectiveInterval(s.cfg.Defaults)

		// Schedule DNS/HTTP check if URL is present
		if strings.TrimSpace(sys.URL) != "" {
			jitter := time.Duration(r.Int63n(int64(interval / 3)))
			s.wg.Add(1)
			go s.runDNSLoop(ctx, sys, jitter, interval)
		}

		// Schedule IP check if IP is present
		if strings.TrimSpace(sys.IP) != "" {
			jitter := time.Duration(r.Int63n(int64(interval / 3)))
			s.wg.Add(1)
			go s.runIPLoop(ctx, sys, jitter, interval)
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
		ctx, cancel := context.WithTimeout(baseCtx, 15*time.Second)
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

func (s *Scheduler) runDNSLoop(ctx context.Context, sys config.SystemConfig, jitter, interval time.Duration) {
	defer s.wg.Done()

	// Initial jitter sleep
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	// First execution
	s.executeDNS(ctx, sys)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.executeDNS(ctx, sys)
		}
	}
}

func (s *Scheduler) executeDNS(ctx context.Context, sys config.SystemConfig) {
	opts := checker.CheckOptions{
		URL:                sys.URL,
		Method:             sys.Method,
		Headers:            sys.Headers,
		HealthPath:         sys.HealthPath,
		ExpectedStatus:     sys.ExpectedStatus,
		ExpectedBody:       sys.ExpectedBody,
		ExpectedDNS:        sys.ExpectedDNS,
		JSONPath:           sys.JSONPath,
		Timeout:            sys.EffectiveHTTPTimeout(s.cfg.Defaults),
		InsecureSkipVerify: sys.InsecureSkipVerify,
	}

	res := s.httpChecker.Check(ctx, opts)
	s.store.RecordDNSResult(sys.ID, res)
}

func (s *Scheduler) runIPLoop(ctx context.Context, sys config.SystemConfig, jitter, interval time.Duration) {
	defer s.wg.Done()

	// Initial jitter sleep
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	// First execution
	s.executeIP(ctx, sys)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.executeIP(ctx, sys)
		}
	}
}

func (s *Scheduler) executeIP(ctx context.Context, sys config.SystemConfig) {
	if sys.IsIPHTTP() {
		opts := checker.CheckOptions{
			URL:                sys.IP,
			Method:             sys.Method,
			Headers:            sys.Headers,
			HealthPath:         sys.HealthPath,
			ExpectedStatus:     sys.ExpectedStatus,
			ExpectedBody:       sys.ExpectedBody,
			JSONPath:           sys.JSONPath,
			Timeout:            sys.EffectiveHTTPTimeout(s.cfg.Defaults),
			InsecureSkipVerify: sys.InsecureSkipVerify,
		}
		res := s.httpChecker.Check(ctx, opts)
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

	s.store.RecordIPResult(sys.ID, res)
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

	for r := range resCh {
		cancel()
		return r, true
	}
	return checker.Result{}, false
}
