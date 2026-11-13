package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPChecker executes DNS resolution and HTTP health verification.
type HTTPChecker struct {
	client *http.Client
	skipInsecureClient *http.Client
}

// NewHTTPChecker creates a reusable HTTP checker with sensible connection pooling.
func NewHTTPChecker() *HTTPChecker {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	insecureTransport := transport.Clone()
	insecureTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec

	redirectPolicy := func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("stopped after 5 redirects")
		}
		return nil
	}

	return &HTTPChecker{
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: redirectPolicy,
		},
		skipInsecureClient: &http.Client{
			Transport:     insecureTransport,
			CheckRedirect: redirectPolicy,
		},
	}
}

// CheckOptions defines execution parameters for Check.
type CheckOptions struct {
	URL                string
	HealthPath         string
	ExpectedStatus     []int
	Timeout            time.Duration
	InsecureSkipVerify bool
}

// Check performs DNS lookup and HTTP request.
func (h *HTTPChecker) Check(ctx context.Context, opts CheckOptions) Result {
	now := time.Now()
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parsedURL, err := url.Parse(opts.URL)
	if err != nil {
		return Result{
			OK:        false,
			ErrorCat:  CatConnect,
			Error:     fmt.Sprintf("invalid URL: %v", err),
			CheckedAt: now,
		}
	}

	host := parsedURL.Hostname()
	// Step 1: DNS resolution check if target is a hostname (not a raw IP).
	if net.ParseIP(host) == nil && host != "localhost" {
		dnsStart := time.Now()
		_, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return Result{
				OK:        false,
				Latency:   time.Since(dnsStart),
				ErrorCat:  CatDNS,
				Error:     fmt.Sprintf("DNS resolution failed for %s: %v", host, err),
				CheckedAt: now,
			}
		}
	}

	targetURL := opts.URL
	if opts.HealthPath != "" {
		if !strings.HasSuffix(targetURL, opts.HealthPath) {
			targetURL = strings.TrimRight(targetURL, "/") + opts.HealthPath
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return Result{
			OK:        false,
			ErrorCat:  CatConnect,
			Error:     fmt.Sprintf("failed to create request: %v", err),
			CheckedAt: now,
		}
	}
	req.Header.Set("User-Agent", "Argus/1.0 (+https://github.com/empresa/argus)")

	client := h.client
	if opts.InsecureSkipVerify {
		client = h.skipInsecureClient
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		cat, errMsg := categorizeHTTPError(ctx, err)
		return Result{
			OK:        false,
			Latency:   latency,
			ErrorCat:  cat,
			Error:     errMsg,
			CheckedAt: now,
		}
	}
	defer func() {
		// Drain and close for keep-alive
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	statusCode := resp.StatusCode
	isCloudflare := strings.EqualFold(resp.Header.Get("Server"), "cloudflare") ||
		resp.Header.Get("CF-RAY") != "" ||
		(statusCode >= 520 && statusCode <= 526)

	// Mode with explicit expected status codes
	if len(opts.ExpectedStatus) > 0 {
		matched := false
		for _, code := range opts.ExpectedStatus {
			if statusCode == code {
				matched = true
				break
			}
		}
		if matched {
			return Result{
				OK:         true,
				StatusCode: statusCode,
				Latency:    latency,
				CheckedAt:  now,
			}
		}
		cat := CatUnexpectedStatus
		if isCloudflare && statusCode >= 500 {
			cat = CatProxy
		} else if statusCode >= 500 {
			cat = CatHTTP5xx
		}
		return Result{
			OK:         false,
			StatusCode: statusCode,
			Latency:    latency,
			ErrorCat:   cat,
			Error:      formatStatusError(statusCode, isCloudflare),
			CheckedAt:  now,
		}
	}

	// Mode with health_path: requires 2xx
	if opts.HealthPath != "" {
		if statusCode >= 200 && statusCode < 300 {
			return Result{
				OK:         true,
				StatusCode: statusCode,
				Latency:    latency,
				CheckedAt:  now,
			}
		}
		cat := CatUnexpectedStatus
		if isCloudflare && statusCode >= 500 {
			cat = CatProxy
		} else if statusCode >= 500 {
			cat = CatHTTP5xx
		}
		return Result{
			OK:         false,
			StatusCode: statusCode,
			Latency:    latency,
			ErrorCat:   cat,
			Error:      formatStatusError(statusCode, isCloudflare),
			CheckedAt:  now,
		}
	}

	// Hybrid base mode without health_path: any status < 500 is OK (server is alive)
	if statusCode < 500 {
		return Result{
			OK:         true,
			StatusCode: statusCode,
			Latency:    latency,
			CheckedAt:  now,
		}
	}

	// 5xx on base URL: failure
	cat := CatHTTP5xx
	if isCloudflare {
		cat = CatProxy
	}
	return Result{
		OK:         false,
		StatusCode: statusCode,
		Latency:    latency,
		ErrorCat:   cat,
		Error:      formatStatusError(statusCode, isCloudflare),
		CheckedAt:  now,
	}
}

func categorizeHTTPError(ctx context.Context, err error) (string, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return CatTimeout, "tempo limite da requisição HTTP excedido"
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return CatTimeout, "timeout de conexão com o servidor"
	}

	var certErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certErr) || errors.As(err, &hostErr) || errors.As(err, &certInvalidErr) ||
		strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "tls:") {
		return CatTLS, fmt.Sprintf("erro de certificado TLS: %v", err)
	}

	if strings.Contains(err.Error(), "connection refused") {
		return CatConnect, "conexão recusada pelo host"
	}

	if strings.Contains(err.Error(), "no such host") {
		return CatDNS, "falha de resolução de nome (DNS)"
	}

	return CatConnect, fmt.Sprintf("falha de conexão: %v", err)
}

func formatStatusError(statusCode int, isCloudflare bool) string {
	if isCloudflare {
		switch statusCode {
		case 502:
			return "Cloudflare 502 — Bad Gateway (origem não respondeu)"
		case 503:
			return "Cloudflare 503 — Service Temporarily Unavailable"
		case 504:
			return "Cloudflare 504 — Gateway Timeout (timeout na origem)"
		case 520:
			return "Cloudflare 520 — Web Server Returned an Unknown Error"
		case 521:
			return "Cloudflare 521 — Web Server Is Down (servidor desligado)"
		case 522:
			return "Cloudflare 522 — Connection Timed Out (timeout de conexão)"
		case 523:
			return "Cloudflare 523 — Origin Is Unreachable"
		case 524:
			return "Cloudflare 524 — A Timeout Occurred"
		case 525:
			return "Cloudflare 525 — SSL Handshake Failed"
		case 526:
			return "Cloudflare 526 — Invalid SSL Certificate"
		default:
			return fmt.Sprintf("Cloudflare %d — erro de borda", statusCode)
		}
	}

	switch statusCode {
	case 500:
		return "HTTP 500 — Internal Server Error"
	case 502:
		return "HTTP 502 — Bad Gateway"
	case 503:
		return "HTTP 503 — Service Unavailable"
	case 504:
		return "HTTP 504 — Gateway Timeout"
	default:
		return fmt.Sprintf("HTTP %d inesperado", statusCode)
	}
}
