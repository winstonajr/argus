package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnv_ExpandEnv(t *testing.T) {
	_ = os.Setenv("TEST_APP_HOST", "app.example.com")
	defer os.Unsetenv("TEST_APP_HOST")

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "https://${TEST_APP_HOST}/health",
			expected: "https://app.example.com/health",
		},
		{
			input:    "https://${UNDEFINED_HOST:-default.com}/ping",
			expected: "https://default.com/ping",
		},
		{
			input:    "https://${TEST_APP_HOST:-fallback.com}/live",
			expected: "https://app.example.com/live",
		},
		{
			input:    "plain string without env",
			expected: "plain string without env",
		},
	}

	for _, tc := range tests {
		actual := ExpandEnv(tc.input)
		if actual != tc.expected {
			t.Errorf("ExpandEnv(%q) = %q; want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestEnv_LoadDotEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := `
# Comment line
ARGUS_TEST_KEY1=value1
ARGUS_TEST_KEY2="quoted value"
ARGUS_TEST_KEY3='single quoted'
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write .env: %v", err)
	}

	defer os.Unsetenv("ARGUS_TEST_KEY1")
	defer os.Unsetenv("ARGUS_TEST_KEY2")
	defer os.Unsetenv("ARGUS_TEST_KEY3")

	if err := LoadDotEnv(envPath); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}

	if val := os.Getenv("ARGUS_TEST_KEY1"); val != "value1" {
		t.Errorf("expected value1, got %q", val)
	}
	if val := os.Getenv("ARGUS_TEST_KEY2"); val != "quoted value" {
		t.Errorf("expected 'quoted value', got %q", val)
	}
	if val := os.Getenv("ARGUS_TEST_KEY3"); val != "single quoted" {
		t.Errorf("expected 'single quoted', got %q", val)
	}
}

func TestEnv_Overrides(t *testing.T) {
	_ = os.Setenv("ARGUS_SERVER_ADDR", ":9999")
	_ = os.Setenv("ARGUS_STORAGE_PATH", "/tmp/argus_custom.db")
	_ = os.Setenv("ARGUS_DEFAULTS_INTERVAL", "45s")
	_ = os.Setenv("ARGUS_DEFAULTS_FAIL_THRESHOLD", "5")
	defer os.Unsetenv("ARGUS_SERVER_ADDR")
	defer os.Unsetenv("ARGUS_STORAGE_PATH")
	defer os.Unsetenv("ARGUS_DEFAULTS_INTERVAL")
	defer os.Unsetenv("ARGUS_DEFAULTS_FAIL_THRESHOLD")

	yamlContent := `
server:
  addr: ":8080"
storage:
  path: "./data/argus.db"
defaults:
  interval: 15s
  fail_threshold: 2
systems:
  - id: s1
    name: "System 1"
    url: "https://example.com"
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Addr != ":9999" {
		t.Errorf("expected server addr :9999, got %q", cfg.Server.Addr)
	}
	if cfg.Storage.Path != "/tmp/argus_custom.db" {
		t.Errorf("expected storage path /tmp/argus_custom.db, got %q", cfg.Storage.Path)
	}
	if cfg.Defaults.Interval != 45*time.Second {
		t.Errorf("expected interval 45s, got %v", cfg.Defaults.Interval)
	}
	if cfg.Defaults.FailThreshold != 5 {
		t.Errorf("expected fail threshold 5, got %d", cfg.Defaults.FailThreshold)
	}
}
