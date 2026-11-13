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
}
