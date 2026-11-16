package config

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var envVarRegex = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::-([^}]*))?\}`)

// LoadDotEnv loads key-value pairs from a .env file into the environment
// without overwriting existing environment variables. If the file does not exist,
// it returns nil without error.
func LoadDotEnv(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Strip quotes if present
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		// Only set if not already present in environment
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// ExpandEnv replaces ${VAR} and ${VAR:-default} occurrences in a string.
func ExpandEnv(content string) string {
	return envVarRegex.ReplaceAllStringFunc(content, func(match string) string {
		sub := envVarRegex.FindStringSubmatch(match)
		if len(sub) >= 2 {
			varName := sub[1]
			if val, ok := os.LookupEnv(varName); ok && val != "" {
				return val
			}
			if len(sub) >= 3 {
				return sub[2]
			}
		}
		return match
	})
}

// applyEnvOverrides overwrites configuration parameters with ARGUS_* or standard env vars.
func (c *Config) applyEnvOverrides() {
	if addr := os.Getenv("ARGUS_SERVER_ADDR"); addr != "" {
		c.Server.Addr = addr
	} else if port := os.Getenv("PORT"); port != "" {
		if !strings.HasPrefix(port, ":") {
			c.Server.Addr = ":" + port
		} else {
			c.Server.Addr = port
		}
	}

	if storagePath := os.Getenv("ARGUS_STORAGE_PATH"); storagePath != "" {
		c.Storage.Path = storagePath
	}
	if retChecks := os.Getenv("ARGUS_STORAGE_RETENTION_CHECKS"); retChecks != "" {
		if d, err := time.ParseDuration(retChecks); err == nil {
			c.Storage.RetentionChecks = d
		}
	}
	if retEvents := os.Getenv("ARGUS_STORAGE_RETENTION_EVENTS"); retEvents != "" {
		if d, err := time.ParseDuration(retEvents); err == nil {
			c.Storage.RetentionEvents = d
		}
	}

	if interval := os.Getenv("ARGUS_DEFAULTS_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			c.Defaults.Interval = d
		}
	}
	if httpTimeout := os.Getenv("ARGUS_DEFAULTS_HTTP_TIMEOUT"); httpTimeout != "" {
		if d, err := time.ParseDuration(httpTimeout); err == nil {
			c.Defaults.HTTPTimeout = d
		}
	}
	if pingTimeout := os.Getenv("ARGUS_DEFAULTS_PING_TIMEOUT"); pingTimeout != "" {
		if d, err := time.ParseDuration(pingTimeout); err == nil {
			c.Defaults.PingTimeout = d
		}
	}
	if failThresh := os.Getenv("ARGUS_DEFAULTS_FAIL_THRESHOLD"); failThresh != "" {
		if n, err := strconv.Atoi(failThresh); err == nil && n > 0 {
			c.Defaults.FailThreshold = n
		}
	}
	if degLat := os.Getenv("ARGUS_DEFAULTS_DEGRADED_LATENCY"); degLat != "" {
		if d, err := time.ParseDuration(degLat); err == nil {
			c.Defaults.DegradedLatency = d
		}
	}
	if tlsWarn := os.Getenv("ARGUS_DEFAULTS_TLS_WARN_DAYS"); tlsWarn != "" {
		if n, err := strconv.Atoi(tlsWarn); err == nil && n > 0 {
			c.Defaults.TLSWarnDays = n
		}
	}

	c.applyAlertEnvOverrides()
}

func (c *Config) applyAlertEnvOverrides() {
	if cooldown := os.Getenv("ARGUS_ALERTS_COOLDOWN"); cooldown != "" {
		if d, err := time.ParseDuration(cooldown); err == nil {
			c.Alerts.Cooldown = d
		}
	}
	if notifyDeg := os.Getenv("ARGUS_ALERTS_NOTIFY_ON_DEGRADED"); notifyDeg != "" {
		if b, err := strconv.ParseBool(notifyDeg); err == nil {
			c.Alerts.NotifyOnDegraded = b
		}
	}

	if v := os.Getenv("ARGUS_ALERTS_WEBHOOK_URL"); v != "" {
		c.Alerts.Webhook.URL = v
	}
	if v := os.Getenv("ARGUS_ALERTS_SLACK_WEBHOOK_URL"); v != "" {
		c.Alerts.Slack.WebhookURL = v
	}
	if v := os.Getenv("ARGUS_ALERTS_DISCORD_WEBHOOK_URL"); v != "" {
		c.Alerts.Discord.WebhookURL = v
	}
	if v := os.Getenv("ARGUS_ALERTS_TELEGRAM_BOT_TOKEN"); v != "" {
		c.Alerts.Telegram.BotToken = v
	}
	if v := os.Getenv("ARGUS_ALERTS_TELEGRAM_CHAT_ID"); v != "" {
		c.Alerts.Telegram.ChatID = v
	}

	if v := os.Getenv("ARGUS_ALERTS_EMAIL_SMTP_HOST"); v != "" {
		c.Alerts.Email.SMTPHost = v
	}
	if v := os.Getenv("ARGUS_ALERTS_EMAIL_SMTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Alerts.Email.SMTPPort = n
		}
	}
	if v := os.Getenv("ARGUS_ALERTS_EMAIL_USERNAME"); v != "" {
		c.Alerts.Email.Username = v
	}
	if v := os.Getenv("ARGUS_ALERTS_EMAIL_PASSWORD"); v != "" {
		c.Alerts.Email.Password = v
	}
	if v := os.Getenv("ARGUS_ALERTS_EMAIL_FROM"); v != "" {
		c.Alerts.Email.From = v
	}
	if v := os.Getenv("ARGUS_ALERTS_EMAIL_TO"); v != "" {
		parts := strings.Split(v, ",")
		recipients := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				recipients = append(recipients, trimmed)
			}
		}
		if len(recipients) > 0 {
			c.Alerts.Email.To = recipients
		}
	}
}
