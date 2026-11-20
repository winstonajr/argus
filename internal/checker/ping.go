package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"argus/internal/i18n"
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
			Error:     i18n.Pickf("falha ao inicializar ping para %s: %v", "failed to init pinger for %s: %v", ipStr, err),
			CheckedAt: now,
		}
	}

	pinger.Count = 3
	pinger.Timeout = timeout
	interval := time.Second
	if timeout > 0 && timeout < 3*time.Second {
		interval = timeout / 4
		if interval < 200*time.Millisecond {
			interval = 200 * time.Millisecond
		}
	}
	pinger.Interval = interval
	pinger.SetPrivileged(true)

	// Run pinger with context cancellation support
	done := make(chan error, 1)
	go func() {
		done <- pinger.Run()
	}()

	select {
	case <-ctx.Done():
		pinger.Stop()
		<-done
		return Result{
			OK:        false,
			Loss:      100,
			ErrorCat:  CatTimeout,
			Error:     i18n.Pick("ping cancelado pelo contexto", "ping canceled by context"),
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
					Error:     i18n.Pick("permissão insuficiente para ICMP (necessário cap_net_raw)", "insufficient permission for ICMP (cap_net_raw required)"),
					CheckedAt: now,
				}
			}
			return Result{
				OK:        false,
				ErrorCat:  CatConnect,
				Error:     i18n.Pickf("falha ao executar ping: %v", "failed to run ping: %v", err),
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
			Error:     i18n.Pickf("100%% perda de pacotes ICMP para %s", "100%% ICMP packet loss to %s", ipStr),
			CheckedAt: now,
		}
	}

	if loss > 0 {
		return Result{
			OK:        true,
			Loss:      loss,
			Latency:   avgRtt,
			ErrorCat:  CatICMPLoss,
			Error:     i18n.Pickf("%.0f%% de perda de pacotes ICMP", "%.0f%% ICMP packet loss", loss),
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
		if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused") {
			return Result{
				OK:        false,
				Latency:   latency,
				ErrorCat:  CatTCPRefused,
				Error:     i18n.Pickf("porta TCP %d recusou conexão", "TCP port %d refused connection", port),
				CheckedAt: now,
			}
		}
		if errorsIsTimeout(err) || ctx.Err() != nil {
			return Result{
				OK:        false,
				Latency:   timeout,
				ErrorCat:  CatTimeout,
				Error:     i18n.Pickf("timeout conectando em %s via TCP", "timeout connecting to %s over TCP", target),
				CheckedAt: now,
			}
		}
		return Result{
			OK:        false,
			Latency:   latency,
			ErrorCat:  CatConnect,
			Error:     i18n.Pickf("falha na conexão TCP: %v", "TCP connection failed: %v", err),
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
