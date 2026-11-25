package monitor

import (
	"context"
	"net"
	"sync"
	"time"
)

const (
	defaultProbeInterval = 10 * time.Second
	defaultProbeTimeout  = 2 * time.Second
	probeCoalesceWindow  = 3 * time.Second
)

var defaultConnectivityTargets = []string{
	"1.1.1.1:443",
	"8.8.8.8:443",
	"1.1.1.1:53",
	"8.8.8.8:53",
}

// ConnectivityStatus represents the host's internet connectivity state.
type ConnectivityStatus struct {
	Offline       bool      `json:"offline"`
	LastChecked   time.Time `json:"last_checked"`
	Since         time.Time `json:"since,omitempty"`
	RecoveredFrom time.Time `json:"-"`
	Transitioned  bool      `json:"-"`
}

// defaultConnectivityProbe checks whether at least one public endpoint accepts a TCP connection.
func defaultConnectivityProbe(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, defaultProbeTimeout)
	defer cancel()

	resCh := make(chan bool, len(defaultConnectivityTargets))
	var wg sync.WaitGroup
	dialer := net.Dialer{Timeout: defaultProbeTimeout}

	for _, addr := range defaultConnectivityTargets {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			conn, err := dialer.DialContext(probeCtx, "tcp", target)
			if err == nil {
				_ = conn.Close()
				resCh <- true
			}
		}(addr)
	}

	go func() {
		wg.Wait()
		close(resCh)
	}()

	for ok := range resCh {
		if ok {
			cancel()
			return true
		}
	}
	return false
}
