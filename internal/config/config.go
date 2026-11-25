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
	RepeatInterval   time.Duration       `yaml:"repeat_interval"`
	NotifyOnDegraded bool                `yaml:"notify_on_degraded"`
	PublicURL        string              `yaml:"public_url"`
	Webhook          WebhookAlertConfig  `yaml:"webhook"`
	Slack            SlackAlertConfig    `yaml:"slack"`
	Discord          DiscordAlertConfig  `yaml:"discord"`
	Telegram         TelegramAlertConfig `yaml:"telegram"`
	Email            EmailAlertConfig    `yaml:"email"`
	Teams            TeamsAlertConfig    `yaml:"teams"`
	Ntfy             NtfyAlertConfig     `yaml:"ntfy"`
	WhatsApp         WhatsAppAlertConfig `yaml:"whatsapp"`
	Routes           []AlertRouteConfig  `yaml:"routes"`
	SLAReport        SLAReportConfig     `yaml:"sla_report"`
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

type TeamsAlertConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

type NtfyAlertConfig struct {
	URL   string `yaml:"url"`
	Topic string `yaml:"topic"`
	Token string `yaml:"token"`
}

type WhatsAppAlertConfig struct {
	URL    string `yaml:"url"`
	Token  string `yaml:"token"`
	Target string `yaml:"target"`
}

// AlertRouteConfig overrides alert destinations for matching systems.
type AlertRouteConfig struct {
	MatchGroups  []string            `yaml:"match_groups"`
	MatchTags    []string            `yaml:"match_tags"`
	MatchSystems []string            `yaml:"match_systems"`
	Webhook      WebhookAlertConfig  `yaml:"webhook"`
	Slack        SlackAlertConfig    `yaml:"slack"`
	Discord      DiscordAlertConfig  `yaml:"discord"`
	Telegram     TelegramAlertConfig `yaml:"telegram"`
	Email        EmailAlertConfig    `yaml:"email"`
	Teams        TeamsAlertConfig    `yaml:"teams"`
	Ntfy         NtfyAlertConfig     `yaml:"ntfy"`
	WhatsApp     WhatsAppAlertConfig `yaml:"whatsapp"`
}

// SLAReportConfig configures periodic SLA summary reports.
type SLAReportConfig struct {
	Schedule string `yaml:"schedule"`
	Day      string `yaml:"day"`
	Hour     string `yaml:"hour"`
}

// MaintenanceWindow defines a recurring weekly maintenance window.
type MaintenanceWindow struct {
	Days  []string `yaml:"days"`
	Start string   `yaml:"start"`
	End   string   `yaml:"end"`
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
	Interval           time.Duration       `yaml:"interval"`
	HTTPTimeout        time.Duration       `yaml:"http_timeout"`
	PingTimeout        time.Duration       `yaml:"ping_timeout"`
	FailThreshold      int                 `yaml:"fail_threshold"`
	DegradedLatency    time.Duration       `yaml:"degraded_latency"`
	TLSWarnDays        int                 `yaml:"tls_warn_days"`
	MaintenanceWindows []MaintenanceWindow `yaml:"maintenance_windows"`
}

// SystemConfig represents one monitored system target.
type SystemConfig struct {
	ID                 string              `yaml:"id"`
	Name               string              `yaml:"name"`
	Group              string              `yaml:"group"`
	Tags               []string            `yaml:"tags"`
	DependsOn          string              `yaml:"depends_on"`
	URL                string              `yaml:"url"`
	Method             string              `yaml:"method"`
	Headers            map[string]string   `yaml:"headers"`
	HealthPath         string              `yaml:"health_path"`
	ExpectedStatus     []int               `yaml:"expected_status"`
	ExpectedBody       string              `yaml:"expected_body"`
	ExpectedDNS        []string            `yaml:"expected_dns"`
	JSONPath           string              `yaml:"json_path"`
	Maintenance        bool                `yaml:"maintenance"`
	MaintenanceWindows []MaintenanceWindow `yaml:"maintenance_windows"`
	IP                 string              `yaml:"ip"`
	HostURL            string              `yaml:"host_url"`
	TCPPort            int                 `yaml:"tcp_port"`
	Interval           time.Duration       `yaml:"interval"`
	HTTPTimeout        time.Duration       `yaml:"http_timeout"`
	PingTimeout        time.Duration       `yaml:"ping_timeout"`
	FailThreshold      int                 `yaml:"fail_threshold"`
	TLSWarnDays        int                 `yaml:"tls_warn_days"`
	InsecureSkipVerify bool                `yaml:"insecure_skip_verify"`
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
	if c.Alerts.RepeatInterval < 0 {
		return errors.New("alerts.repeat_interval cannot be negative")
	}
	if err := c.Alerts.SLAReport.validate(); err != nil {
		return err
	}
	for i := range c.Defaults.MaintenanceWindows {
		if err := c.Defaults.MaintenanceWindows[i].validate(); err != nil {
			return fmt.Errorf("defaults.maintenance_windows[%d]: %w", i, err)
		}
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
		sys.DependsOn = strings.TrimSpace(sys.DependsOn)
		for wi := range sys.MaintenanceWindows {
			if err := sys.MaintenanceWindows[wi].validate(); err != nil {
				return fmt.Errorf("system %q maintenance_windows[%d]: %w", sys.ID, wi, err)
			}
		}
	}

	if err := validateDependencies(c.Systems, seenIDs); err != nil {
		return err
	}

	return nil
}

func validateDependencies(systems []SystemConfig, seenIDs map[string]bool) error {
	parentOf := make(map[string]string, len(systems))
	for _, sys := range systems {
		if sys.DependsOn == "" {
			continue
		}
		if sys.DependsOn == sys.ID {
			return fmt.Errorf("system %q cannot depend on itself", sys.ID)
		}
		if !seenIDs[sys.DependsOn] {
			return fmt.Errorf("system %q depends_on unknown system %q", sys.ID, sys.DependsOn)
		}
		parentOf[sys.ID] = sys.DependsOn
	}

	// 0 = unvisited, 1 = visiting, 2 = visited
	state := make(map[string]int, len(parentOf))
	for id := range parentOf {
		if state[id] != 0 {
			continue
		}
		curr := id
		for curr != "" {
			if state[curr] == 2 {
				break
			}
			if state[curr] == 1 {
				return fmt.Errorf("dependency cycle detected involving system %q", curr)
			}
			state[curr] = 1
			curr = parentOf[curr]
		}
		for curr2 := id; curr2 != "" && state[curr2] == 1; curr2 = parentOf[curr2] {
			state[curr2] = 2
		}
	}
	return nil
}

// EffectiveMaintenanceWindows returns global and system-specific maintenance windows.
func (s *SystemConfig) EffectiveMaintenanceWindows(d DefaultsConfig) []MaintenanceWindow {
	if len(d.MaintenanceWindows) == 0 {
		return s.MaintenanceWindows
	}
	if len(s.MaintenanceWindows) == 0 {
		return d.MaintenanceWindows
	}
	merged := make([]MaintenanceWindow, 0, len(d.MaintenanceWindows)+len(s.MaintenanceWindows))
	merged = append(merged, d.MaintenanceWindows...)
	merged = append(merged, s.MaintenanceWindows...)
	return merged
}

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

func (w *MaintenanceWindow) validate() error {
	if len(w.Days) == 0 {
		return errors.New("days cannot be empty")
	}
	for i, d := range w.Days {
		norm := strings.ToLower(strings.TrimSpace(d))
		if norm != "*" {
			if _, ok := weekdayNames[norm]; !ok {
				return fmt.Errorf("invalid day %q (use mon..sun or *)", d)
			}
		}
		w.Days[i] = norm
	}
	startMin, err := parseHHMM(w.Start)
	if err != nil {
		return fmt.Errorf("invalid start %q: %w", w.Start, err)
	}
	endMin, err := parseHHMM(w.End)
	if err != nil {
		return fmt.Errorf("invalid end %q: %w", w.End, err)
	}
	if startMin == endMin {
		return errors.New("start and end cannot be identical")
	}
	w.Start = strings.TrimSpace(w.Start)
	w.End = strings.TrimSpace(w.End)
	return nil
}

// Contains reports whether t falls inside the recurring maintenance window.
func (w MaintenanceWindow) Contains(t time.Time) bool {
	startMin, err1 := parseHHMM(w.Start)
	endMin, err2 := parseHHMM(w.End)
	if err1 != nil || err2 != nil || startMin == endMin {
		return false
	}
	curMin := t.Hour()*60 + t.Minute()
	if startMin < endMin {
		return w.matchesWeekday(t.Weekday()) && curMin >= startMin && curMin < endMin
	}
	// Midnight wrap: e.g. 23:00 -> 01:00
	if curMin >= startMin {
		return w.matchesWeekday(t.Weekday())
	}
	if curMin < endMin {
		prevDay := (t.Weekday() + 6) % 7
		return w.matchesWeekday(prevDay)
	}
	return false
}

func (w MaintenanceWindow) matchesWeekday(wd time.Weekday) bool {
	for _, d := range w.Days {
		if d == "*" {
			return true
		}
		if mapped, ok := weekdayNames[d]; ok && mapped == wd {
			return true
		}
	}
	return false
}

func parseHHMM(s string) (int, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, errors.New("expected HH:MM")
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, errors.New("expected HH:MM")
	}
	return t.Hour()*60 + t.Minute(), nil
}

func (s *SLAReportConfig) validate() error {
	s.Schedule = strings.ToLower(strings.TrimSpace(s.Schedule))
	s.Day = strings.ToLower(strings.TrimSpace(s.Day))
	s.Hour = strings.TrimSpace(s.Hour)
	switch s.Schedule {
	case "":
		return nil
	case "weekly":
		if s.Day == "" {
			s.Day = "mon"
		}
		if _, ok := weekdayNames[s.Day]; !ok {
			return fmt.Errorf("alerts.sla_report.day %q must be mon..sun for weekly schedule", s.Day)
		}
	case "monthly":
		if s.Day == "" {
			s.Day = "1"
		}
		var dayNum int
		if _, err := fmt.Sscanf(s.Day, "%d", &dayNum); err != nil || dayNum < 1 || dayNum > 28 {
			return fmt.Errorf("alerts.sla_report.day %q must be 1..28 for monthly schedule", s.Day)
		}
	default:
		return fmt.Errorf("alerts.sla_report.schedule %q must be weekly or monthly", s.Schedule)
	}
	if s.Hour == "" {
		s.Hour = "08:00"
	}
	if _, err := parseHHMM(s.Hour); err != nil {
		return fmt.Errorf("alerts.sla_report.hour %q: %w", s.Hour, err)
	}
	return nil
}
