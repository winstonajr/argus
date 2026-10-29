package checker

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestPingChecker_TCP(t *testing.T) {
	// 1. Success case: start a local TCP listener
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	_, portStr, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	checker := NewPingChecker()
	res := checker.CheckTCP(context.Background(), "127.0.0.1", port, 1*time.Second)
	if !res.OK {
		t.Errorf("expected TCP check OK, got error: %s", res.Error)
	}

	// 2. Connection refused case: pick an unused closed port
	_ = l.Close()
	resRefused := checker.CheckTCP(context.Background(), "127.0.0.1", port, 1*time.Second)
	if resRefused.OK {
		t.Errorf("expected failure on closed port, got OK")
	}
	if resRefused.ErrorCat != CatTCPRefused {
		t.Errorf("expected CatTCPRefused, got %q", resRefused.ErrorCat)
	}
}

func TestPingChecker_ICMP_InvalidIP(t *testing.T) {
	checker := NewPingChecker()
	res := checker.CheckICMP(context.Background(), "invalid-ip", 500*time.Millisecond)
	if res.OK {
		t.Errorf("expected invalid IP to fail")
	}
}
