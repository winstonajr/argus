package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// PingChecker provides ICMP and TCP ping methods.
type PingChecker struct{}

// NewPingChecker creates a new PingChecker.
func NewPingChecker() *PingChecker {
	return &PingChecker{}
}

// CheckICMP pings an IP using ICMP packets via pro-bing in privileged mode.
func (p *PingChecker) CheckICMP(ctx context.Context, ipStr string, timeout time.Duration) Result {
	now := time.Now()
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	pinger, err := probing.NewPinger(ipStr)
	if err != nil {
		return Result{
			OK:        false,
			ErrorCat:  CatConnect,
			Error:     fmt.Sprintf("failed to init pinger for %s: %v", ipStr, err),
			CheckedAt: now,
		}
	}

	pinger.Count = 3
	pinger.Timeout = timeout
	pinger.SetPrivileged(true)

	// Run pinger with context cancellation support
	done := make(chan error, 1)
	go func() {
		done <- pinger.Run()
	}()

	select {
	case <-ctx.Done():
		pinger.Stop()
		return Result{
			OK:        false,
			Loss:      100,
			ErrorCat:  CatTimeout,
			Error:     "ping cancelado pelo contexto",
			CheckedAt: now,
		}
	case err := <-done:
		if err != nil {
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "operation not permitted") ||
				strings.Contains(errStr, "permission denied") ||
				strings.Contains(errStr, "socket: permission") {
				return Result{
					OK:        false,
					ErrorCat:  CatPermission,
					Error:     "permissão insuficiente para ICMP (necessário cap_net_raw)",
					CheckedAt: now,
				}
			}
			return Result{
				OK:        false,
				ErrorCat:  CatConnect,
				Error:     fmt.Sprintf("falha ao executar ping: %v", err),
				CheckedAt: now,
			}
		}
	}

	stats := pinger.Statistics()
	loss := stats.PacketLoss
	avgRtt := stats.AvgRtt

	if stats.PacketsRecv == 0 {
		return Result{
			OK:        false,
			Loss:      100,
			Latency:   timeout,
			ErrorCat:  CatICMPTimeout,
			Error:     fmt.Sprintf("100%% perda de pacotes ICMP para %s", ipStr),
			CheckedAt: now,
		}
	}

	if loss > 0 {
		return Result{
			OK:        true,
			Loss:      loss,
			Latency:   avgRtt,
			ErrorCat:  CatICMPLoss,
			Error:     fmt.Sprintf("%.0f%% de perda de pacotes ICMP", loss),
			CheckedAt: now,
		}
	}

	return Result{
		OK:        true,
		Loss:      0,
		Latency:   avgRtt,
		CheckedAt: now,
	}
}

// CheckTCP connects to ip:port via TCP to test host reachability when ICMP is blocked.
func (p *PingChecker) CheckTCP(ctx context.Context, ipStr string, port int, timeout time.Duration) Result {
	now := time.Now()
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	target := net.JoinHostPort(ipStr, fmt.Sprintf("%d", port))
	dialer := &net.Dialer{
		Timeout: timeout,
	}

	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", target)
	latency := time.Since(start)

	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return Result{
				OK:        false,
				Latency:   latency,
				ErrorCat:  CatTCPRefused,
				Error:     fmt.Sprintf("porta TCP %d recusou conexão", port),
				CheckedAt: now,
			}
		}
		if errorsIsTimeout(err) || ctx.Err() != nil {
			return Result{
				OK:        false,
				Latency:   timeout,
				ErrorCat:  CatTimeout,
				Error:     fmt.Sprintf("timeout conectando em %s via TCP", target),
				CheckedAt: now,
			}
		}
		return Result{
			OK:        false,
			Latency:   latency,
			ErrorCat:  CatConnect,
			Error:     fmt.Sprintf("falha na conexão TCP: %v", err),
			CheckedAt: now,
		}
	}
	_ = conn.Close()

	return Result{
		OK:        true,
		Latency:   latency,
		CheckedAt: now,
	}
}

func errorsIsTimeout(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline")
}
