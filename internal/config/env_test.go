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

func TestEnv_AlertOverrides(t *testing.T) {
	envs := map[string]string{
		"ARGUS_DEFAULTS_TLS_WARN_DAYS":     "21",
		"ARGUS_ALERTS_COOLDOWN":            "10m",
		"ARGUS_ALERTS_NOTIFY_ON_DEGRADED":  "true",
		"ARGUS_ALERTS_WEBHOOK_URL":         "https://hooks.example.com/argus",
		"ARGUS_ALERTS_SLACK_WEBHOOK_URL":   "https://hooks.slack.com/services/T/B/X",
		"ARGUS_ALERTS_DISCORD_WEBHOOK_URL": "https://discord.com/api/webhooks/1/2",
		"ARGUS_ALERTS_TELEGRAM_BOT_TOKEN":  "123:ABC",
		"ARGUS_ALERTS_TELEGRAM_CHAT_ID":    "-10012345",
		"ARGUS_ALERTS_EMAIL_SMTP_HOST":     "smtp.example.com",
		"ARGUS_ALERTS_EMAIL_SMTP_PORT":     "587",
		"ARGUS_ALERTS_EMAIL_USERNAME":      "argus@example.com",
		"ARGUS_ALERTS_EMAIL_PASSWORD":      "secret",
		"ARGUS_ALERTS_EMAIL_FROM":          "argus@example.com",
		"ARGUS_ALERTS_EMAIL_TO":            "ops@example.com, sre@example.com ",
	}
	for k, v := range envs {
		_ = os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	yamlContent := `
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

	if cfg.Defaults.TLSWarnDays != 21 {
		t.Errorf("expected TLSWarnDays 21, got %d", cfg.Defaults.TLSWarnDays)
	}
	if cfg.Alerts.Cooldown != 10*time.Minute {
		t.Errorf("expected cooldown 10m, got %v", cfg.Alerts.Cooldown)
	}
	if !cfg.Alerts.NotifyOnDegraded {
		t.Errorf("expected NotifyOnDegraded true")
	}
	if cfg.Alerts.Webhook.URL != envs["ARGUS_ALERTS_WEBHOOK_URL"] {
		t.Errorf("unexpected webhook url: %q", cfg.Alerts.Webhook.URL)
	}
	if cfg.Alerts.Slack.WebhookURL != envs["ARGUS_ALERTS_SLACK_WEBHOOK_URL"] {
		t.Errorf("unexpected slack url: %q", cfg.Alerts.Slack.WebhookURL)
	}
	if cfg.Alerts.Discord.WebhookURL != envs["ARGUS_ALERTS_DISCORD_WEBHOOK_URL"] {
		t.Errorf("unexpected discord url: %q", cfg.Alerts.Discord.WebhookURL)
	}
	if cfg.Alerts.Telegram.BotToken != "123:ABC" || cfg.Alerts.Telegram.ChatID != "-10012345" {
		t.Errorf("unexpected telegram config: %+v", cfg.Alerts.Telegram)
	}
	if cfg.Alerts.Email.SMTPHost != "smtp.example.com" || cfg.Alerts.Email.SMTPPort != 587 {
		t.Errorf("unexpected email host/port: %+v", cfg.Alerts.Email)
	}
	if len(cfg.Alerts.Email.To) != 2 || cfg.Alerts.Email.To[0] != "ops@example.com" || cfg.Alerts.Email.To[1] != "sre@example.com" {
		t.Errorf("unexpected email recipients: %+v", cfg.Alerts.Email.To)
	}
}
