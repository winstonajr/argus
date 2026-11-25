package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_ValidConfig(t *testing.T) {
	yamlContent := `
server:
  addr: ":9090"
storage:
  path: "./test.db"
defaults:
  interval: 20s
systems:
  - id: erp
    name: "ERP System"
    url: "https://erp.example.com"
    health_path: "/healthz"
    ip: "192.168.1.10"
  - id: portal
    ip: "10.0.0.1"
    tcp_port: 80
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected valid load, got error: %v", err)
	}

	if cfg.Server.Addr != ":9090" {
		t.Errorf("expected addr :9090, got %s", cfg.Server.Addr)
	}
	if cfg.Defaults.Interval != 20*time.Second {
		t.Errorf("expected default interval 20s, got %v", cfg.Defaults.Interval)
	}
	if len(cfg.Systems) != 2 {
		t.Fatalf("expected 2 systems, got %d", len(cfg.Systems))
	}

	erp := cfg.Systems[0]
	if erp.Name != "ERP System" {
		t.Errorf("expected name 'ERP System', got %s", erp.Name)
	}
	if erp.EffectiveInterval(cfg.Defaults) != 20*time.Second {
		t.Errorf("expected effective interval 20s, got %v", erp.EffectiveInterval(cfg.Defaults))
	}

	portal := cfg.Systems[1]
	if portal.Name != "portal" {
		t.Errorf("expected fallback name 'portal', got %s", portal.Name)
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name: "empty addr",
			modify: func(c *Config) {
				c.Server.Addr = ""
			},
			wantErr: "server.addr cannot be empty",
		},
		{
			name: "interval too short",
			modify: func(c *Config) {
				c.Defaults.Interval = 5 * time.Second
			},
			wantErr: "defaults.interval must be at least 10s",
		},
		{
			name: "no systems",
			modify: func(c *Config) {
				c.Systems = nil
			},
			wantErr: "at least one system must be configured",
		},
		{
			name: "duplicate id",
			modify: func(c *Config) {
				c.Systems = append(c.Systems, SystemConfig{ID: "sys-1", IP: "127.0.0.1"})
			},
			wantErr: "duplicate system id",
		},
		{
			name: "invalid id characters",
			modify: func(c *Config) {
				c.Systems[0].ID = "bad id with spaces!"
			},
			wantErr: "is invalid (use letters, numbers, hyphens, underscores)",
		},
		{
			name: "neither url nor ip",
			modify: func(c *Config) {
				c.Systems[0].URL = ""
				c.Systems[0].IP = ""
			},
			wantErr: "must have at least 'url' or 'ip'",
		},
		{
			name: "invalid url scheme",
			modify: func(c *Config) {
				c.Systems[0].URL = "ftp://example.com"
			},
			wantErr: "must be http:// or https://",
		},
		{
			name: "health_path without slash",
			modify: func(c *Config) {
				c.Systems[0].HealthPath = "health"
			},
			wantErr: "must start with '/'",
		},
		{
			name: "invalid ip",
			modify: func(c *Config) {
				c.Systems[0].IP = "999.999.999.999"
			},
			wantErr: "has invalid ip",
		},
		{
			name: "tcp_port without ip",
			modify: func(c *Config) {
				c.Systems[0].IP = ""
				c.Systems[0].TCPPort = 80
			},
			wantErr: "tcp_port specified but no ip configured",
		},
		{
			name: "tcp_port out of range",
			modify: func(c *Config) {
				c.Systems[0].TCPPort = 70000
			},
			wantErr: "tcp_port 70000 must be between 1 and 65535",
		},
		{
			name: "tcp_port with http host url",
			modify: func(c *Config) {
				c.Systems[0].IP = "http://10.0.0.1"
				c.Systems[0].TCPPort = 8080
			},
			wantErr: "tcp_port cannot be specified when ip/host is an http url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Systems = []SystemConfig{
				{ID: "sys-1", URL: "https://example.com", IP: "1.1.1.1"},
			}
			tt.modify(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error %q to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConfig_HTTPHost(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Systems = []SystemConfig{
		{ID: "sys-http-ip", IP: "http://10.0.0.159"},
		{ID: "sys-incomplete-scheme", IP: "http:10.0.0.132"},
		{ID: "sys-host-url", HostURL: "http://10.60.0.128:8080"},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to be valid, got: %v", err)
	}

	if cfg.Systems[0].IP != "http://10.0.0.159" || !cfg.Systems[0].IsIPHTTP() {
		t.Errorf("expected http://10.0.0.159 and IsIPHTTP true, got %s", cfg.Systems[0].IP)
	}
	if cfg.Systems[1].IP != "http://10.0.0.132" || !cfg.Systems[1].IsIPHTTP() {
		t.Errorf("expected normalized http://10.0.0.132, got %s", cfg.Systems[1].IP)
	}
	if cfg.Systems[2].IP != "http://10.60.0.128:8080" || !cfg.Systems[2].IsIPHTTP() {
		t.Errorf("expected host_url mapped to http://10.60.0.128:8080, got %s", cfg.Systems[2].IP)
	}
}

func TestConfig_TCPPortDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Systems = []SystemConfig{
		{ID: "ip-default", IP: "10.0.0.10"},
		{ID: "ip-custom", IP: "10.0.0.11", TCPPort: 443},
		{ID: "url-only", URL: "https://example.com"},
		{ID: "http-host", IP: "http://10.0.0.12:8080"},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to be valid, got: %v", err)
	}

	if cfg.Systems[0].TCPPort != 0 {
		t.Errorf("expected default tcp_port 0 (ICMP), got %d", cfg.Systems[0].TCPPort)
	}
	if cfg.Systems[1].TCPPort != 443 {
		t.Errorf("expected custom tcp_port 443, got %d", cfg.Systems[1].TCPPort)
	}
	if cfg.Systems[2].TCPPort != 0 {
		t.Errorf("expected tcp_port 0 for url-only system, got %d", cfg.Systems[2].TCPPort)
	}
	if cfg.Systems[3].TCPPort != 0 {
		t.Errorf("expected tcp_port 0 for http-host system, got %d", cfg.Systems[3].TCPPort)
	}
}

func contains(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr || len(substr) == 0 || (len(str) > 0 && len(substr) > 0 && stringContains(str, substr)))
}

func TestSantaEasterEggDefaultAndEnvOverride(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.SantaEasterEgg {
		t.Fatalf("expected Server.SantaEasterEgg default to be false")
	}

	t.Setenv("ARGUS_SANTA_EASTER_EGG", "true")
	cfg.applyEnvOverrides()
	if !cfg.Server.SantaEasterEgg {
		t.Fatalf("expected ARGUS_SANTA_EASTER_EGG=true to enable SantaEasterEgg")
	}

	t.Setenv("ARGUS_SANTA_EASTER_EGG", "false")
	cfg.applyEnvOverrides()
	if cfg.Server.SantaEasterEgg {
		t.Fatalf("expected ARGUS_SANTA_EASTER_EGG=false to disable SantaEasterEgg")
	}
}

func TestSeasonalThemesDefaultAndEnvOverride(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Server.SeasonalThemes {
		t.Fatalf("expected Server.SeasonalThemes default to be true")
	}

	t.Setenv("ARGUS_SEASONAL_THEMES", "false")
	cfg.applyEnvOverrides()
	if cfg.Server.SeasonalThemes {
		t.Fatalf("expected ARGUS_SEASONAL_THEMES=false to disable SeasonalThemes")
	}

	t.Setenv("ARGUS_SEASONAL_THEMES", "true")
	cfg.applyEnvOverrides()
	if !cfg.Server.SeasonalThemes {
		t.Fatalf("expected ARGUS_SEASONAL_THEMES=true to enable SeasonalThemes")
	}
}

func TestTimezoneConfigAndEnvOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Systems = []SystemConfig{{ID: "sys1", URL: "https://example.com"}}
	cfg.Server.Timezone = "America/Sao_Paulo"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid timezone, got: %v", err)
	}

	cfg.Server.Timezone = "Invalid/Zone_Name"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for invalid timezone")
	}

	t.Setenv("ARGUS_TIMEZONE", "Europe/Lisbon")
	cfg.applyEnvOverrides()
	if cfg.Server.Timezone != "Europe/Lisbon" {
		t.Fatalf("expected ARGUS_TIMEZONE override to set Europe/Lisbon, got %q", cfg.Server.Timezone)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid overridden timezone, got: %v", err)
	}
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
