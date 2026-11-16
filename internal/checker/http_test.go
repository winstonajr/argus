package checker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPChecker_Modes(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		healthPath     string
		expectedStatus []int
		wantOK         bool
		wantCat        string
	}{
		{
			name: "Base mode 200 is OK",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantOK:  true,
			wantCat: CatNone,
		},
		{
			name: "Base mode 404 is OK (server responded)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantOK:  true,
			wantCat: CatNone,
		},
		{
			name: "Base mode 500 is failure",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantOK:  false,
			wantCat: CatHTTP5xx,
		},
		{
			name: "Cloudflare 502 Bad Gateway is CatProxy",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Server", "cloudflare")
				w.WriteHeader(http.StatusBadGateway)
			},
			wantOK:  false,
			wantCat: CatProxy,
		},
		{
			name: "Cloudflare 521 is CatProxy",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(521)
			},
			wantOK:  false,
			wantCat: CatProxy,
		},
		{
			name: "Health path 200 is OK",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			healthPath: "/health",
			wantOK:     true,
			wantCat:    CatNone,
		},
		{
			name: "Health path 404 is failure (requires 2xx)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			healthPath: "/health",
			wantOK:     false,
			wantCat:    CatUnexpectedStatus,
		},
		{
			name: "ExpectedStatus list match",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			},
			expectedStatus: []int{http.StatusTeapot, http.StatusOK},
			wantOK:         true,
			wantCat:        CatNone,
		},
		{
			name: "ExpectedStatus list mismatch",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			expectedStatus: []int{http.StatusOK},
			wantOK:         false,
			wantCat:        CatUnexpectedStatus,
		},
	}

	checker := NewHTTPChecker()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(tt.handler)
			defer ts.Close()

			res := checker.Check(context.Background(), CheckOptions{
				URL:            ts.URL,
				HealthPath:     tt.healthPath,
				ExpectedStatus: tt.expectedStatus,
				Timeout:        2 * time.Second,
			})

			if res.OK != tt.wantOK {
				t.Errorf("got OK=%v, want %v (error: %s)", res.OK, tt.wantOK, res.Error)
			}
			if res.ErrorCat != tt.wantCat {
				t.Errorf("got ErrorCat=%q, want %q", res.ErrorCat, tt.wantCat)
			}
		})
	}
}

func TestHTTPChecker_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	checker := NewHTTPChecker()
	res := checker.Check(context.Background(), CheckOptions{
		URL:     ts.URL,
		Timeout: 50 * time.Millisecond,
	})

	if res.OK {
		t.Errorf("expected failure on timeout, got OK")
	}
	if res.ErrorCat != CatTimeout {
		t.Errorf("expected CatTimeout, got %q", res.ErrorCat)
	}
}

func TestHTTPChecker_DNSFailure(t *testing.T) {
	checker := NewHTTPChecker()
	res := checker.Check(context.Background(), CheckOptions{
		URL:     "http://nonexistent-domain-argus-12345.local",
		Timeout: 1 * time.Second,
	})

	if res.OK {
		t.Errorf("expected failure for nonexistent domain, got OK")
	}
	if res.ErrorCat != CatDNS {
		t.Errorf("expected CatDNS, got %q", res.ErrorCat)
	}
}

func TestHTTPChecker_MethodHeadersBodyAndTLS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy","db":"ok"}`))
	}))
	defer ts.Close()

	c := NewHTTPChecker()

	// 1. Substring match + TLS info
	res := c.Check(context.Background(), CheckOptions{
		URL:                ts.URL,
		Method:             "POST",
		Headers:            map[string]string{"Authorization": "Bearer secret"},
		ExpectedBody:       `"status":"healthy"`,
		InsecureSkipVerify: true,
		Timeout:            2 * time.Second,
	})
	if !res.OK {
		t.Fatalf("expected OK, got error: %s", res.Error)
	}
	if !res.HasTLS {
		t.Errorf("expected HasTLS=true for TLS server")
	}

	// 2. Regex match
	resRegex := c.Check(context.Background(), CheckOptions{
		URL:                ts.URL,
		Method:             "POST",
		Headers:            map[string]string{"Authorization": "Bearer secret"},
		ExpectedBody:       `/"db"\s*:\s*"ok"/`,
		InsecureSkipVerify: true,
		Timeout:            2 * time.Second,
	})
	if !resRegex.OK {
		t.Fatalf("expected regex body match to succeed, got %s", resRegex.Error)
	}

	// 3. Body mismatch
	resFail := c.Check(context.Background(), CheckOptions{
		URL:                ts.URL,
		Method:             "POST",
		Headers:            map[string]string{"Authorization": "Bearer secret"},
		ExpectedBody:       `"status":"down"`,
		InsecureSkipVerify: true,
		Timeout:            2 * time.Second,
	})
	if resFail.OK || resFail.ErrorCat != CatUnexpectedBody {
		t.Errorf("expected CatUnexpectedBody failure, got OK=%v cat=%q", resFail.OK, resFail.ErrorCat)
	}
}

func TestHTTPChecker_ExpectedDNS_JSONPath_And_ExpiredTLS(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"database":"up","healthy":true}`))
	}))
	defer ts.Close()

	c := NewHTTPChecker()

	// 1. JSONPath match
	resOK := c.Check(context.Background(), CheckOptions{
		URL:      ts.URL,
		JSONPath: "$.database == 'up'",
		Timeout:  2 * time.Second,
	})
	if !resOK.OK {
		t.Fatalf("expected JSONPath match to succeed, got error: %s", resOK.Error)
	}

	// 2. JSONPath mismatch
	resJSONFail := c.Check(context.Background(), CheckOptions{
		URL:      ts.URL,
		JSONPath: "$.database == 'down'",
		Timeout:  2 * time.Second,
	})
	if resJSONFail.OK || resJSONFail.ErrorCat != CatUnexpectedBody {
		t.Errorf("expected CatUnexpectedBody for JSONPath mismatch, got OK=%v cat=%q", resJSONFail.OK, resJSONFail.ErrorCat)
	}

	// 3. ExpectedDNS mismatch (using localhost URL resolving to 127.0.0.1 / ::1)
	resDNSFail := c.Check(context.Background(), CheckOptions{
		URL:         "http://localhost:1",
		ExpectedDNS: []string{"192.0.2.99"},
		Timeout:     2 * time.Second,
	})
	if resDNSFail.OK || resDNSFail.ErrorCat != CatDNS {
		t.Errorf("expected CatDNS for ExpectedDNS mismatch, got OK=%v cat=%q", resDNSFail.OK, resDNSFail.ErrorCat)
	}

	// 4. Expired TLS cert (< 24h ago) must yield TLSDaysLeft < 0 (not 0)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-6 * time.Hour),
		DNSNames:     []string{"127.0.0.1"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	tlsSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	tlsSrv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
	}
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	resExpTLS := c.Check(context.Background(), CheckOptions{
		URL:                tlsSrv.URL,
		InsecureSkipVerify: true,
		Timeout:            2 * time.Second,
	})
	if !resExpTLS.HasTLS || resExpTLS.TLSDaysLeft >= 0 {
		t.Errorf("expected HasTLS=true and TLSDaysLeft < 0 for expired cert, got HasTLS=%v TLSDaysLeft=%d", resExpTLS.HasTLS, resExpTLS.TLSDaysLeft)
	}
}
