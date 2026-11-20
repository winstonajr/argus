package monitor

import (
	"testing"

	"argus/internal/checker"
	"argus/internal/i18n"
)

func TestDiagnose_HTTPFailures(t *testing.T) {
	i18n.SetDefault(i18n.LangEN)
	t.Cleanup(func() { i18n.SetDefault(i18n.LangPT) })

	failing := func(cat, msg string, code int) *CheckSnapshot {
		return &CheckSnapshot{
			Configured: true,
			Status:     StatusDown,
			LastResult: checker.Result{ErrorCat: cat, Error: msg, StatusCode: code},
		}
	}
	notConfigured := &CheckSnapshot{Configured: false}
	pingUp := &CheckSnapshot{Configured: true, Status: StatusUP, LastResult: checker.Result{OK: true}}

	tests := []struct {
		name        string
		dns, ip     *CheckSnapshot
		wantSummary string
		wantDetail  string
	}{
		{"url-only connect", failing(checker.CatConnect, "connection refused by host", 0), notConfigured, "Service unresponsive", "connection refused by host"},
		{"url-only timeout nil ip", failing(checker.CatTimeout, "HTTP request timed out", 0), nil, "Service unresponsive", "HTTP request timed out"},
		{"connect with ping up", failing(checker.CatConnect, "boom", 0), pingUp, "Service unresponsive", "HTTP service unavailable (boom), but VM responds to ping"},
		{"unexpected status on url", failing(checker.CatUnexpectedStatus, "unexpected HTTP 404", 404), notConfigured, "Unexpected HTTP status", "unexpected HTTP 404"},
		{"ip-only http 5xx", notConfigured, failing(checker.CatHTTP5xx, "HTTP 503 — Service Unavailable", 503), "Application down", "HTTP 503 — Service Unavailable"},
		{"ip-only unexpected status", nil, failing(checker.CatUnexpectedStatus, "unexpected HTTP 403", 403), "Unexpected HTTP status", "unexpected HTTP 403"},
		{"ip-only icmp timeout", nil, failing(checker.CatICMPTimeout, "no reply", 0), "VM or network down", "Host unreachable: no reply"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary, detail := Diagnose(tc.dns, tc.ip)
			if summary != tc.wantSummary || detail != tc.wantDetail {
				t.Errorf("got (%q, %q), want (%q, %q)", summary, detail, tc.wantSummary, tc.wantDetail)
			}
		})
	}
}
