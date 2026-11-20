package checker

import (
	"bytes"
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
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"argus/internal/i18n"
)

// HTTPChecker executes DNS resolution and HTTP health verification.
type HTTPChecker struct {
	client             *http.Client
	skipInsecureClient *http.Client
}

// NewHTTPChecker creates a reusable HTTP checker with sensible connection pooling.
func NewHTTPChecker() *HTTPChecker {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
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
	ExpectedBodyRegex  *regexp.Regexp
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
			Error:     i18n.Pickf("URL inválida: %v", "invalid URL: %v", err),
			CheckedAt: now,
		}
	}

	host := parsedURL.Hostname()
	if len(opts.ExpectedDNS) > 0 {
		dnsStart := time.Now()
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return Result{
				OK:        false,
				Latency:   time.Since(dnsStart),
				ErrorCat:  CatDNS,
				Error:     i18n.Pickf("falha de resolução DNS para %s: %v", "DNS resolution failed for %s: %v", host, err),
				CheckedAt: now,
			}
		}
		if !containsAnyIP(addrs, opts.ExpectedDNS) {
			return Result{
				OK:        false,
				Latency:   time.Since(dnsStart),
				ErrorCat:  CatDNS,
				Error:     i18n.Pickf("DNS %s resolveu para %v (esperado %v)", "DNS %s resolved to %v (expected %v)", host, addrs, opts.ExpectedDNS),
				CheckedAt: now,
			}
		}
	}

	targetURL := opts.URL
	if opts.HealthPath != "" {
		if u, err := url.Parse(targetURL); err == nil {
			if !strings.HasSuffix(u.Path, opts.HealthPath) {
				u.Path = strings.TrimRight(u.Path, "/") + opts.HealthPath
				targetURL = u.String()
			}
		} else if !strings.HasSuffix(targetURL, opts.HealthPath) {
			targetURL = strings.TrimRight(targetURL, "/") + opts.HealthPath
		}
	}

	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = http.MethodGet
	}

	var traceMu sync.Mutex
	var dnsStart, connStart, tlsStart, reqStart time.Time
	var dnsDur, connDur, tlsDur, ttfbDur time.Duration
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			traceMu.Lock()
			dnsStart = time.Now()
			traceMu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			traceMu.Lock()
			if !dnsStart.IsZero() {
				dnsDur = time.Since(dnsStart)
			}
			traceMu.Unlock()
		},
		ConnectStart: func(_, _ string) {
			traceMu.Lock()
			connStart = time.Now()
			traceMu.Unlock()
		},
		ConnectDone: func(_, _ string, _ error) {
			traceMu.Lock()
			if !connStart.IsZero() {
				connDur = time.Since(connStart)
			}
			traceMu.Unlock()
		},
		TLSHandshakeStart: func() {
			traceMu.Lock()
			tlsStart = time.Now()
			traceMu.Unlock()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			traceMu.Lock()
			if !tlsStart.IsZero() {
				tlsDur = time.Since(tlsStart)
			}
			traceMu.Unlock()
		},
		GotFirstResponseByte: func() {
			traceMu.Lock()
			if !reqStart.IsZero() {
				ttfbDur = time.Since(reqStart)
			}
			traceMu.Unlock()
		},
	}

	reqCtx := httptrace.WithClientTrace(ctx, trace)
	req, err := http.NewRequestWithContext(reqCtx, method, targetURL, nil)
	if err != nil {
		return Result{
			OK:        false,
			ErrorCat:  CatConnect,
			Error:     i18n.Pickf("falha ao criar requisição: %v", "failed to create request: %v", err),
			CheckedAt: now,
		}
	}
	req.Header.Set("User-Agent", "Argus/1.0 (+https://github.com/winstonajr/argus)")
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
	traceMu.Lock()
	reqStart = start
	traceMu.Unlock()
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

	if opts.ExpectedBody != "" && !matchExpectedBody(bodyBytes, opts.ExpectedBody, opts.ExpectedBodyRegex) {
		return Result{
			OK:          false,
			StatusCode:  statusCode,
			Latency:     latency,
			HasTLS:      hasTLS,
			TLSDaysLeft: tlsDaysLeft,
			ErrorCat:    CatUnexpectedBody,
			Error:       i18n.Pick("corpo da resposta não corresponde ao esperado", "response body did not match expected pattern"),
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
			Error:       i18n.Pickf("validação JSON (%s) falhou", "JSON validation (%s) failed", opts.JSONPath),
			CheckedAt:   now,
		}
	}

	traceMu.Lock()
	res := Result{
		OK:             true,
		StatusCode:     statusCode,
		Latency:        latency,
		DNSLatency:     dnsDur,
		ConnectLatency: connDur,
		TLSLatency:     tlsDur,
		TTFBLatency:    ttfbDur,
		HasTLS:         hasTLS,
		TLSDaysLeft:    tlsDaysLeft,
		CheckedAt:      now,
	}
	traceMu.Unlock()
	return res
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
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
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

func matchExpectedBody(body []byte, expected string, precompiled *regexp.Regexp) bool {
	if precompiled != nil {
		return precompiled.Match(body)
	}
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
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) || strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "lookup ") {
		return CatDNS, i18n.Pick("falha de resolução de nome (DNS)", "DNS name resolution failed")
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return CatTimeout, i18n.Pick("tempo limite da requisição HTTP excedido", "HTTP request timed out")
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return CatTimeout, i18n.Pick("timeout de conexão com o servidor", "server connection timed out")
	}

	var certErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certErr) || errors.As(err, &hostErr) || errors.As(err, &certInvalidErr) ||
		strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "tls:") {
		return CatTLS, i18n.Pickf("erro de certificado TLS: %v", "TLS certificate error: %v", err)
	}

	if strings.Contains(err.Error(), "connection refused") {
		return CatConnect, i18n.Pick("conexão recusada pelo host", "connection refused by host")
	}

	return CatConnect, i18n.Pickf("falha de conexão: %v", "connection failed: %v", err)
}

func formatStatusError(statusCode int, isCloudflare bool) string {
	if isCloudflare {
		switch statusCode {
		case 502:
			return i18n.Pick("Cloudflare 502 — Bad Gateway (origem não respondeu)", "Cloudflare 502 — Bad Gateway (origin did not respond)")
		case 503:
			return "Cloudflare 503 — Service Temporarily Unavailable"
		case 504:
			return i18n.Pick("Cloudflare 504 — Gateway Timeout (timeout na origem)", "Cloudflare 504 — Gateway Timeout (origin timed out)")
		case 520:
			return "Cloudflare 520 — Web Server Returned an Unknown Error"
		case 521:
			return i18n.Pick("Cloudflare 521 — Web Server Is Down (servidor desligado)", "Cloudflare 521 — Web Server Is Down")
		case 522:
			return i18n.Pick("Cloudflare 522 — Connection Timed Out (timeout de conexão)", "Cloudflare 522 — Connection Timed Out")
		case 523:
			return "Cloudflare 523 — Origin Is Unreachable"
		case 524:
			return "Cloudflare 524 — A Timeout Occurred"
		case 525:
			return "Cloudflare 525 — SSL Handshake Failed"
		case 526:
			return "Cloudflare 526 — Invalid SSL Certificate"
		default:
			return i18n.Pickf("Cloudflare %d — erro de borda", "Cloudflare %d — edge error", statusCode)
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
		return i18n.Pickf("HTTP %d inesperado", "unexpected HTTP %d", statusCode)
	}
}
