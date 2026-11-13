package checker

import (
	"context"
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
