package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
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
	Method             string
	Headers            map[string]string
	HealthPath         string
	ExpectedStatus     []int
	ExpectedBody       string
	ExpectedDNS        []string
	JSONPath           string
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
	// Step 1: DNS resolution check if target is a hostname (not a raw IP) or ExpectedDNS is set.
	if (net.ParseIP(host) == nil && host != "localhost") || len(opts.ExpectedDNS) > 0 {
		dnsStart := time.Now()
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return Result{
				OK:        false,
				Latency:   time.Since(dnsStart),
				ErrorCat:  CatDNS,
				Error:     fmt.Sprintf("DNS resolution failed for %s: %v", host, err),
				CheckedAt: now,
			}
		}
		if len(opts.ExpectedDNS) > 0 && !containsAnyIP(addrs, opts.ExpectedDNS) {
			return Result{
				OK:        false,
				Latency:   time.Since(dnsStart),
				ErrorCat:  CatDNS,
				Error:     fmt.Sprintf("DNS %s resolveu para %v (esperado %v)", host, addrs, opts.ExpectedDNS),
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

	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		return Result{
			OK:        false,
			ErrorCat:  CatConnect,
			Error:     fmt.Sprintf("failed to create request: %v", err),
			CheckedAt: now,
		}
	}
	req.Header.Set("User-Agent", "Argus/1.0 (+https://github.com/empresa/argus)")
	for k, v := range opts.Headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}

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

	var bodyBytes []byte
	if (opts.ExpectedBody != "" || opts.JSONPath != "") && method != http.MethodHead {
		bodyBytes, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	}
	_ = resp.Body.Close()

	var hasTLS bool
	var tlsDaysLeft int
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		hasTLS = true
		tlsDaysLeft = int(math.Floor(time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24))
	}

	statusCode := resp.StatusCode
	isCloudflare := strings.EqualFold(resp.Header.Get("Server"), "cloudflare") ||
		resp.Header.Get("CF-RAY") != "" ||
		(statusCode >= 520 && statusCode <= 526)

	statusOK := false
	if len(opts.ExpectedStatus) > 0 {
		for _, code := range opts.ExpectedStatus {
			if statusCode == code {
				statusOK = true
				break
			}
		}
	} else if opts.HealthPath != "" {
		statusOK = statusCode >= 200 && statusCode < 300
	} else {
		statusOK = statusCode < 500
	}

	if !statusOK {
		cat := CatUnexpectedStatus
		if isCloudflare && statusCode >= 500 {
			cat = CatProxy
		} else if statusCode >= 500 {
			cat = CatHTTP5xx
		}
		return Result{
			OK:          false,
			StatusCode:  statusCode,
			Latency:     latency,
			HasTLS:      hasTLS,
			TLSDaysLeft: tlsDaysLeft,
			ErrorCat:    cat,
			Error:       formatStatusError(statusCode, isCloudflare),
			CheckedAt:   now,
		}
	}

	if opts.ExpectedBody != "" && !matchExpectedBody(bodyBytes, opts.ExpectedBody) {
		return Result{
			OK:          false,
			StatusCode:  statusCode,
			Latency:     latency,
			HasTLS:      hasTLS,
			TLSDaysLeft: tlsDaysLeft,
			ErrorCat:    CatUnexpectedBody,
			Error:       "corpo da resposta não corresponde ao esperado",
			CheckedAt:   now,
		}
	}

	if opts.JSONPath != "" && !matchJSONPath(bodyBytes, opts.JSONPath) {
		return Result{
			OK:          false,
			StatusCode:  statusCode,
			Latency:     latency,
			HasTLS:      hasTLS,
			TLSDaysLeft: tlsDaysLeft,
			ErrorCat:    CatUnexpectedBody,
			Error:       fmt.Sprintf("validação JSON (%s) falhou", opts.JSONPath),
			CheckedAt:   now,
		}
	}

	return Result{
		OK:          true,
		StatusCode:  statusCode,
		Latency:     latency,
		HasTLS:      hasTLS,
		TLSDaysLeft: tlsDaysLeft,
		CheckedAt:   now,
	}
}

func containsAnyIP(resolved, expected []string) bool {
	for _, r := range resolved {
		for _, e := range expected {
			if strings.TrimSpace(r) == strings.TrimSpace(e) {
				return true
			}
		}
	}
	return false
}

func matchJSONPath(body []byte, expr string) bool {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return false
	}

	expr = strings.TrimSpace(expr)
	var pathPart, op, wantVal string
	if idx := strings.Index(expr, "=="); idx > 0 {
		pathPart = strings.TrimSpace(expr[:idx])
		op = "=="
		wantVal = strings.TrimSpace(expr[idx+2:])
	} else if idx := strings.Index(expr, "!="); idx > 0 {
		pathPart = strings.TrimSpace(expr[:idx])
		op = "!="
		wantVal = strings.TrimSpace(expr[idx+2:])
	} else {
		pathPart = expr
	}

	pathPart = strings.TrimPrefix(pathPart, "$.")
	pathPart = strings.TrimPrefix(pathPart, "$")
	if pathPart == "" {
		return false
	}

	cur := data
	for _, seg := range strings.Split(pathPart, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		val, exists := m[seg]
		if !exists {
			return false
		}
		cur = val
	}

	if op == "" {
		return cur != nil
	}

	wantVal = strings.Trim(wantVal, `"'`)
	gotStr := fmt.Sprintf("%v", cur)
	if op == "==" {
		return gotStr == wantVal
	}
	return gotStr != wantVal
}

func matchExpectedBody(body []byte, expected string) bool {
	if strings.HasPrefix(expected, "/") && strings.HasSuffix(expected, "/") && len(expected) > 2 {
		re, err := regexp.Compile(expected[1 : len(expected)-1])
		if err != nil {
			return false
		}
		return re.Match(body)
	}
	return strings.Contains(string(body), expected)
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
