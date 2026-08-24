package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// loadConfig loads configuration from an explicit path if provided, otherwise
// searches several conventional locations to work when launched by external hosts (e.g., MCP clients).
// Priority (highest to lowest): CLI flags > env vars > config file > defaults.
// Env vars use the prefix HOUSEKEEPER_ with dots replaced by underscores, e.g.:
//
//	HOUSEKEEPER_CLICKHOUSE_HOST, HOUSEKEEPER_CLICKHOUSE_PASSWORD, HOUSEKEEPER_HTTP_AUTH_TOKEN
func loadConfig(explicitPath string) error {
	// Enable environment variable support
	viper.SetEnvPrefix("HOUSEKEEPER")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Set defaults for all configuration values
	// These can be overridden by env vars, config file, or command-line flags
	viper.SetDefault("clickhouse.host", "127.0.0.1")
	viper.SetDefault("clickhouse.port", 9000)
	viper.SetDefault("clickhouse.user", "default")
	viper.SetDefault("clickhouse.password", "")
	viper.SetDefault("clickhouse.database", "default")
	viper.SetDefault("clickhouse.cluster", "default")

	viper.SetDefault("prometheus.host", "localhost")
	viper.SetDefault("prometheus.port", 8481)
	viper.SetDefault("prometheus.vm_cluster_mode", false)
	viper.SetDefault("prometheus.vm_tenant_id", "0")
	viper.SetDefault("prometheus.vm_path_prefix", "")

	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "text")

	viper.SetDefault("http.addr", ":8080")
	viper.SetDefault("http.auth_token", "")

	// Deployment-specific guidance. extra_tool_description is shared facts
	// appended to BOTH clickhouse_query and the diagnose agent (topology,
	// clusters, attribution columns, query patterns). query_extra_description is
	// appended ONLY to clickhouse_query — for restricted-route caveats (column
	// REVOKEs, etc.) that don't apply to the elevated diagnose connection.
	viper.SetDefault("mcp.extra_tool_description", "")
	viper.SetDefault("mcp.query_extra_description", "")

	// Bedrock-backed in-MCP diagnose tool. Empty region/model_id disables the
	// diagnose tool. model_id is a Bedrock model or inference-profile
	// id, set per deployment (e.g. via HOUSEKEEPER_BEDROCK_MODEL_ID). Credentials
	// come from the default AWS credential chain.
	viper.SetDefault("bedrock.region", "")
	viper.SetDefault("bedrock.model_id", "")
	viper.SetDefault("bedrock.max_tokens", 2048)
	viper.SetDefault("bedrock.max_iterations", 8)
	// Wall-clock CAP for a diagnosis; once exceeded the agent stops calling
	// tools and returns a summary so the MCP client doesn't time out. 0 disables.
	viper.SetDefault("bedrock.max_seconds", 25)
	// Default per-call budget when the caller doesn't pass budget_seconds.
	// Sized so budget + the in-flight turn + one summarize turn fits a fixed
	// ~60s client tool timeout at Sonnet-5-class per-turn latency; callers with
	// larger timeouts opt up per call, clamped to max_seconds.
	viper.SetDefault("bedrock.default_seconds", 35)
	// Async jobs (clickhouse_diagnose_async): default budget when the caller
	// doesn't pass budget_seconds (clamped to max_seconds), and how many
	// investigations may run concurrently.
	viper.SetDefault("bedrock.async_default_seconds", 120)
	viper.SetDefault("bedrock.max_concurrent_jobs", 3)
	// < 0 = don't send temperature. Newer Anthropic models (Sonnet 5+) reject
	// the parameter on Converse; set >= 0 only for older models that accept it.
	viper.SetDefault("bedrock.temperature", -1)

	// Optional separate ClickHouse connection used only by the server-side
	// diagnose agent. Empty fields fall back to the clickhouse.* connection.
	viper.SetDefault("analyst_clickhouse.host", "")
	viper.SetDefault("analyst_clickhouse.port", 0)
	viper.SetDefault("analyst_clickhouse.user", "")
	viper.SetDefault("analyst_clickhouse.password", "")
	viper.SetDefault("analyst_clickhouse.database", "")

	if explicitPath == "" {
		if env := os.Getenv("HOUSEKEEPER_CONFIG"); env != "" {
			explicitPath = env
		}
	}

	if explicitPath != "" {
		viper.SetConfigFile(explicitPath)
		if err := viper.ReadInConfig(); err != nil {
			// Don't fail if config file doesn't exist when flags are provided
			logrus.WithError(err).Debug("Could not read config file, using defaults and flags")
		} else {
			logrus.WithField("config_file", viper.ConfigFileUsed()).Debug("Loaded config file")
		}
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")

		// Relative to working dir
		viper.AddConfigPath(".")
		viper.AddConfigPath("configs")

		// Relative to executable dir
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			viper.AddConfigPath(dir)
			viper.AddConfigPath(filepath.Join(dir, "configs"))
		}

		// XDG/home
		if home, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(filepath.Join(home, ".config", "housekeeper"))
		}

		// System path
		viper.AddConfigPath("/etc/housekeeper")

		// Try to read config, but don't fail if not found
		if err := viper.ReadInConfig(); err != nil {
			logrus.WithError(err).Debug("No config file found, using defaults and flags")
		} else {
			logrus.WithField("config_file", viper.ConfigFileUsed()).Debug("Loaded config file")
		}
	}

	// Configure logging after config is loaded
	configureLogging()
	return nil
}

// configureLogging sets up logrus based on configuration
func configureLogging() {
	// Set log level
	level := viper.GetString("logging.level")
	if level == "" {
		level = "info"
	}

	parsedLevel, err := logrus.ParseLevel(strings.ToLower(level))
	if err != nil {
		logrus.WithError(err).Warn("Invalid log level, defaulting to info")
		parsedLevel = logrus.InfoLevel
	}
	logrus.SetLevel(parsedLevel)

	// Set log format
	format := viper.GetString("logging.format")
	if strings.ToLower(format) == "json" {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}

	logrus.WithFields(logrus.Fields{
		"level":  level,
		"format": format,
	}).Debug("Logging configured")
}
