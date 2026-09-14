// Package config loads service settings from environment variables (and an
// optional .env file). Variable names are kept identical to the Python
// implementation so existing deployments keep working.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Settings mirrors app/config.py Settings field-by-field.
type Settings struct {
	DatabaseURL string

	RcloneRCURL     string
	RcloneRCUser    string
	RcloneRCPass    string
	RcloneManaged   bool
	RcloneBin       string
	RcloneRCAddr    string
	PollInterval    int // seconds
	CheckTimeout    int // seconds
	APIHost         string
	APIPort         int
	PIDFile         string
	LogLevel        string
	StaticDir       string
	AlertmanagerURL string

	// --- active-standby HA ---
	NodeID            string // "" → "<hostname>-<pid>" at elector construction
	ClusterName       string
	HeartbeatInterval int // seconds
	LeaseDuration     int // seconds
	PeerProbeEnabled  bool
	Peers             string

	// --- auth / RBAC ---
	JWTSecret              string
	JWTAlgorithm           string
	JWTExpiresMin          int
	BcryptRounds           int
	BootstrapAdminUser     string
	BootstrapAdminPassword string
}

// Load reads .env (no override of real env) then env vars with Python-parity
// defaults. Unknown .env keys are ignored (pydantic extra="ignore").
func Load() Settings {
	_ = godotenv.Load() // missing .env is fine
	return Settings{
		DatabaseURL: envStr("DATABASE_URL", "sqlite:///./rclone_sync.db"),

		RcloneRCURL:   envStr("RCLONE_RC_URL", "http://localhost:5572"),
		RcloneRCUser:  envStr("RCLONE_RC_USER", "admin"),
		RcloneRCPass:  envStr("RCLONE_RC_PASS", "6051"),
		RcloneManaged: envBool("RCLONE_MANAGED", true),
		RcloneBin:     envStr("RCLONE_BIN", "rclone"),
		RcloneRCAddr:  envStr("RCLONE_RC_ADDR", "0.0.0.0:5572"),
		PollInterval:  envInt("POLL_INTERVAL_SECONDS", 10),
		CheckTimeout:  envInt("CHECK_TIMEOUT_SECONDS", 3600),
		APIHost:       envStr("API_HOST", "0.0.0.0"),
		APIPort:       envInt("API_PORT", 8000),
		PIDFile:       envStr("PID_FILE", "rclone-sync.pid"),
		LogLevel:      envStr("LOG_LEVEL", "INFO"),
		StaticDir:     envStr("STATIC_DIR", "web/dist"),

		NodeID:            envStr("NODE_ID", ""),
		ClusterName:       envStr("CLUSTER_NAME", "default"),
		HeartbeatInterval: envInt("HEARTBEAT_INTERVAL_SECONDS", 3),
		LeaseDuration:     envInt("LEASE_DURATION_SECONDS", 10),
		PeerProbeEnabled:  envBool("PEER_PROBE_ENABLED", false),
		Peers:             envStr("PEERS", ""),

		JWTSecret:              envStr("JWT_SECRET", "CHANGE-ME-IN-PRODUCTION"),
		JWTAlgorithm:           envStr("JWT_ALGORITHM", "HS256"),
		JWTExpiresMin:          envInt("JWT_EXPIRES_MINUTES", 480),
		BcryptRounds:           envInt("BCRYPT_ROUNDS", 12),
		BootstrapAdminUser:     envStr("BOOTSTRAP_ADMIN_USER", ""),
		BootstrapAdminPassword: envStr("BOOTSTRAP_ADMIN_PASSWORD", ""),

		AlertmanagerURL: envStr("ALERTMANAGER_URL", ""),
	}
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// LogLevelSlog maps the Python-style level name to a slog Level.
// Unknown values fall back to Info (a typo never silently swallows logs).
func (s Settings) LogLevelSlog() slog.Level {
	switch upper(s.LogLevel) {
	case "CRITICAL", "FATAL", "ERROR":
		return slog.LevelError
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "DEBUG":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// Validate reports fatal startup misconfigurations (uvicorn-parity).
func (s Settings) Validate() error {
	if s.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is empty. Set it to 32+ random bytes via env or .env. Generate one with: openssl rand -hex 32")
	}
	return nil
}

func upper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}
