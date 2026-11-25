package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"argus/internal/i18n"
	"gopkg.in/yaml.v3"
)

var idRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Config defines the top-level configuration for Argus.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Defaults DefaultsConfig `yaml:"defaults"`
	Alerts   AlertsConfig   `yaml:"alerts"`
	Systems  []SystemConfig `yaml:"systems"`
}

// AlertsConfig holds notification channel settings.
type AlertsConfig struct {
	Cooldown         time.Duration       `yaml:"cooldown"`
	NotifyOnDegraded bool                `yaml:"notify_on_degraded"`
	PublicURL        string              `yaml:"public_url"`
	Webhook          WebhookAlertConfig  `yaml:"webhook"`
	Slack            SlackAlertConfig    `yaml:"slack"`
	Discord          DiscordAlertConfig  `yaml:"discord"`
	Telegram         TelegramAlertConfig `yaml:"telegram"`
	Email            EmailAlertConfig    `yaml:"email"`
}

type WebhookAlertConfig struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}

type SlackAlertConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

type DiscordAlertConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

type TelegramAlertConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

type EmailAlertConfig struct {
	SMTPHost string   `yaml:"smtp_host"`
	SMTPPort int      `yaml:"smtp_port"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	From     string   `yaml:"from"`
	To       []string `yaml:"to"`
}

// AuthConfig holds optional HTTP Basic Auth and Bearer Token credentials.
type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Token    string `yaml:"token"`
}

// Enabled returns true when authentication is configured.
func (a AuthConfig) Enabled() bool {
	return strings.TrimSpace(a.Password) != "" || strings.TrimSpace(a.Token) != ""
}

// ServerConfig holds HTTP server parameters.
type ServerConfig struct {
	Addr           string     `yaml:"addr"`
	Lang           string     `yaml:"lang"`
	Timezone       string     `yaml:"timezone"`
	Auth           AuthConfig `yaml:"auth"`
	SantaEasterEgg bool       `yaml:"santa_easter_egg"`
	SeasonalThemes bool       `yaml:"seasonal_themes"`
}

// StorageConfig holds SQLite storage parameters and retention settings.
type StorageConfig struct {
	Path            string        `yaml:"path"`
	RetentionChecks time.Duration `yaml:"retention_checks"`
	RetentionEvents time.Duration `yaml:"retention_events"`
}

// DefaultsConfig contains fallback values for system checkers.
type DefaultsConfig struct {
	Interval        time.Duration `yaml:"interval"`
	HTTPTimeout     time.Duration `yaml:"http_timeout"`
	PingTimeout     time.Duration `yaml:"ping_timeout"`
	FailThreshold   int           `yaml:"fail_threshold"`
	DegradedLatency time.Duration `yaml:"degraded_latency"`
	TLSWarnDays     int           `yaml:"tls_warn_days"`
}

// SystemConfig represents one monitored system target.
type SystemConfig struct {
	ID                 string            `yaml:"id"`
	Name               string            `yaml:"name"`
	Group              string            `yaml:"group"`
	Tags               []string          `yaml:"tags"`
	URL                string            `yaml:"url"`
	Method             string            `yaml:"method"`
	Headers            map[string]string `yaml:"headers"`
	HealthPath         string            `yaml:"health_path"`
	ExpectedStatus     []int             `yaml:"expected_status"`
	ExpectedBody       string            `yaml:"expected_body"`
	ExpectedDNS        []string          `yaml:"expected_dns"`
	JSONPath           string            `yaml:"json_path"`
	Maintenance        bool              `yaml:"maintenance"`
	IP                 string            `yaml:"ip"`
	HostURL            string            `yaml:"host_url"`
	TCPPort            int               `yaml:"tcp_port"`
	Interval           time.Duration     `yaml:"interval"`
	HTTPTimeout        time.Duration     `yaml:"http_timeout"`
	PingTimeout        time.Duration     `yaml:"ping_timeout"`
	FailThreshold      int               `yaml:"fail_threshold"`
	TLSWarnDays        int               `yaml:"tls_warn_days"`
	InsecureSkipVerify bool              `yaml:"insecure_skip_verify"`
	bodyRegex          *regexp.Regexp
}

// BodyRegex returns the pre-compiled regex when ExpectedBody is formatted as /pattern/.
func (s *SystemConfig) BodyRegex() *regexp.Regexp {
	return s.bodyRegex
}

// EffectiveInterval returns system specific interval or default.
func (s *SystemConfig) EffectiveInterval(d DefaultsConfig) time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return d.Interval
}

// EffectiveHTTPTimeout returns system specific timeout or default.
func (s *SystemConfig) EffectiveHTTPTimeout(d DefaultsConfig) time.Duration {
	if s.HTTPTimeout > 0 {
		return s.HTTPTimeout
	}
	return d.HTTPTimeout
}

// EffectivePingTimeout returns system specific ping timeout or default.
func (s *SystemConfig) EffectivePingTimeout(d DefaultsConfig) time.Duration {
	if s.PingTimeout > 0 {
		return s.PingTimeout
	}
	return d.PingTimeout
}

// EffectiveFailThreshold returns system specific threshold or default.
func (s *SystemConfig) EffectiveFailThreshold(d DefaultsConfig) int {
	if s.FailThreshold > 0 {
		return s.FailThreshold
	}
	return d.FailThreshold
}

// EffectiveTLSWarnDays returns system specific TLS warning threshold or default.
func (s *SystemConfig) EffectiveTLSWarnDays(d DefaultsConfig) int {
	if s.TLSWarnDays > 0 {
		return s.TLSWarnDays
	}
	if d.TLSWarnDays > 0 {
		return d.TLSWarnDays
	}
	return 15
}

// IsIPHTTP returns true if IP is configured as an HTTP or HTTPS endpoint.
func (s *SystemConfig) IsIPHTTP() bool {
	trimmed := strings.TrimSpace(s.IP)
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

// Load reads and parses configuration from a YAML file.
// It supports environment interpolation (${VAR} or ${VAR:-default}) and ARGUS_* env overrides.
func Load(path string) (*Config, error) {
	_ = LoadDotEnv(".env")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	expandedData := []byte(ExpandEnv(string(data)))

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(expandedData, cfg); err != nil {
		return nil, fmt.Errorf("parse config yaml %q: %w", path, err)
	}

	cfg.applyEnvOverrides()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// DefaultConfig provides sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:           ":8080",
			Lang:           i18n.LangPT,
			SantaEasterEgg: false,
			SeasonalThemes: true,
		},
		Storage: StorageConfig{
			Path:            "./data/argus.db",
			RetentionChecks: 168 * time.Hour,  // 7 days
			RetentionEvents: 2160 * time.Hour, // 90 days
		},
		Defaults: DefaultsConfig{
			Interval:        30 * time.Second,
			HTTPTimeout:     5 * time.Second,
			PingTimeout:     3 * time.Second,
			FailThreshold:   2,
			DegradedLatency: 2 * time.Second,
			TLSWarnDays:     15,
		},
		Alerts: AlertsConfig{
			Cooldown: 5 * time.Minute,
		},
		Systems: []SystemConfig{},
	}
}

// Validate ensures configuration invariants are met.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Server.Addr) == "" {
		return errors.New("server.addr cannot be empty")
	}
	c.Server.Lang = i18n.Normalize(c.Server.Lang)
	i18n.SetDefault(c.Server.Lang)

	c.Server.Timezone = strings.TrimSpace(c.Server.Timezone)
	if c.Server.Timezone != "" {
		if _, err := time.LoadLocation(c.Server.Timezone); err != nil {
			return fmt.Errorf("invalid server.timezone %q: %w", c.Server.Timezone, err)
		}
	}

	if strings.TrimSpace(c.Storage.Path) == "" {
		return errors.New("storage.path cannot be empty")
	}

	if c.Defaults.Interval < 10*time.Second {
		return fmt.Errorf("defaults.interval must be at least 10s (got %v)", c.Defaults.Interval)
	}
	if c.Defaults.HTTPTimeout <= 0 {
		return errors.New("defaults.http_timeout must be positive")
	}
	if c.Defaults.PingTimeout <= 0 {
		return errors.New("defaults.ping_timeout must be positive")
	}
	if c.Defaults.FailThreshold < 1 {
		return errors.New("defaults.fail_threshold must be >= 1")
	}
	if c.Defaults.DegradedLatency < 0 {
		return errors.New("defaults.degraded_latency cannot be negative")
	}
	if c.Defaults.TLSWarnDays < 0 {
		return errors.New("defaults.tls_warn_days cannot be negative")
	}
	if c.Alerts.Cooldown < 0 {
		return errors.New("alerts.cooldown cannot be negative")
	}
	if c.Storage.RetentionChecks < 0 || c.Storage.RetentionEvents < 0 {
		return errors.New("storage retention durations cannot be negative")
	}

	if len(c.Systems) == 0 {
		return errors.New("at least one system must be configured in systems list")
	}

	seenIDs := make(map[string]bool)

	for i := range c.Systems {
		sys := &c.Systems[i]

		sys.ID = strings.TrimSpace(sys.ID)
		if sys.ID == "" {
			return fmt.Errorf("systems[%d].id is required", i)
		}
		if !idRegex.MatchString(sys.ID) {
			return fmt.Errorf("systems[%d].id %q is invalid (use letters, numbers, hyphens, underscores)", i, sys.ID)
		}
		if seenIDs[sys.ID] {
			return fmt.Errorf("duplicate system id: %q", sys.ID)
		}
		seenIDs[sys.ID] = true

		if strings.TrimSpace(sys.Name) == "" {
			sys.Name = sys.ID
		}

		sys.URL = strings.TrimSpace(sys.URL)
		sys.IP = strings.TrimSpace(sys.IP)
		sys.HostURL = strings.TrimSpace(sys.HostURL)

		if sys.IP == "" && sys.HostURL != "" {
			sys.IP = sys.HostURL
		}

		// Normalize incomplete scheme like http:10.0.0.0 or https:10.0.0.0
		if strings.HasPrefix(sys.IP, "http:") && !strings.HasPrefix(sys.IP, "http://") {
			sys.IP = "http://" + strings.TrimPrefix(sys.IP, "http:")
		} else if strings.HasPrefix(sys.IP, "https:") && !strings.HasPrefix(sys.IP, "https://") {
			sys.IP = "https://" + strings.TrimPrefix(sys.IP, "https:")
		}

		if sys.URL == "" && sys.IP == "" {
			return fmt.Errorf("system %q must have at least 'url' or 'ip'", sys.ID)
		}

		if sys.URL != "" {
			u, err := url.ParseRequestURI(sys.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("system %q has invalid url %q (must be http:// or https://)", sys.ID, sys.URL)
			}
		}

		sys.Group = strings.TrimSpace(sys.Group)
		sys.Method = strings.ToUpper(strings.TrimSpace(sys.Method))
		if sys.Method != "" {
			switch sys.Method {
			case "GET", "HEAD", "POST", "PUT", "OPTIONS":
			default:
				return fmt.Errorf("system %q has unsupported HTTP method %q", sys.ID, sys.Method)
			}
		}

		if strings.HasPrefix(sys.ExpectedBody, "/") && strings.HasSuffix(sys.ExpectedBody, "/") && len(sys.ExpectedBody) > 2 {
			pattern := sys.ExpectedBody[1 : len(sys.ExpectedBody)-1]
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("system %q has invalid expected_body regex %q: %w", sys.ID, sys.ExpectedBody, err)
			}
			sys.bodyRegex = re
		} else {
			sys.bodyRegex = nil
		}

		if sys.HealthPath != "" {
			if !strings.HasPrefix(sys.HealthPath, "/") {
				return fmt.Errorf("system %q health_path %q must start with '/'", sys.ID, sys.HealthPath)
			}
		}

		if sys.IP != "" {
			if sys.IsIPHTTP() {
				u, err := url.ParseRequestURI(sys.IP)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("system %q has invalid host url %q (must be http:// or https://)", sys.ID, sys.IP)
				}
				if sys.TCPPort != 0 {
					return fmt.Errorf("system %q tcp_port cannot be specified when ip/host is an http url", sys.ID)
				}
			} else {
				if parsedIP := net.ParseIP(sys.IP); parsedIP == nil {
					return fmt.Errorf("system %q has invalid ip %q", sys.ID, sys.IP)
				}
			}
		}

		if sys.TCPPort != 0 {
			if sys.TCPPort < 1 || sys.TCPPort > 65535 {
				return fmt.Errorf("system %q tcp_port %d must be between 1 and 65535", sys.ID, sys.TCPPort)
			}
			if sys.IP == "" {
				return fmt.Errorf("system %q tcp_port specified but no ip configured", sys.ID)
			}
		}

		if sys.Interval < 0 {
			return fmt.Errorf("system %q interval cannot be negative", sys.ID)
		}
		if sys.Interval > 0 && sys.Interval < 10*time.Second {
			return fmt.Errorf("system %q interval %v must be at least 10s", sys.ID, sys.Interval)
		}
		if sys.HTTPTimeout < 0 || sys.PingTimeout < 0 || sys.TLSWarnDays < 0 {
			return fmt.Errorf("system %q timeouts and tls_warn_days cannot be negative", sys.ID)
		}

		if sys.FailThreshold < 0 {
			return fmt.Errorf("system %q fail_threshold cannot be negative", sys.ID)
		}
	}

	return nil
}
