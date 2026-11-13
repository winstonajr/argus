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
	s.cancel = cancel

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
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
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
		HealthPath:         sys.HealthPath,
		ExpectedStatus:     sys.ExpectedStatus,
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
	}

	s.store.RecordIPResult(sys.ID, res)
}
