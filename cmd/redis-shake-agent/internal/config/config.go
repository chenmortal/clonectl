// Package config loads the agent's YAML configuration file.
//
// The agent is intentionally minimal — it has no DB, no leader
// election, no scheduler. clonectl owns all of those concerns; the
// agent only knows:
//
//   - how to listen on bind_addr with X-Agent-Token auth
//   - where to find the upstream redis-shake / redis-full-check
//     binaries (binary_paths)
//   - where to put per-task working directories, log files, and
//     result.db (work_root / log_root)
//   - which upstream version is currently pinned (so an upstream
//     release can be staged without touching clonectl)
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings is the parsed YAML config. Field tags use snake_case to match
// the YAML keys we expose.
type Settings struct {
	// BindAddr is the listen address, e.g. ":9010" or "127.0.0.1:9010".
	BindAddr string `yaml:"bind_addr"`

	// AuthToken is the shared secret sent as X-Agent-Token. Empty
	// disables auth (dev / LAN only). v2 will replace this with mTLS.
	AuthToken string `yaml:"auth_token"`

	// MaxConcurrentTasks is a semaphore cap on simultaneous fork/exec
	// tasks. 0 means unlimited (caller should set a sane number).
	MaxConcurrentTasks int `yaml:"max_concurrent_tasks"`

	// WorkRoot holds per-task subdirectories: shake.toml, result.db,
	// captured stdout/stderr ringbuffer files, and PID files.
	WorkRoot string `yaml:"work_root"`

	// LogRoot is where the agent writes its own stdout (separate from
	// per-task child logs).
	LogRoot string `yaml:"log_root"`

	// ShutdownGraceSeconds is the deadline between SIGTERM and SIGKILL
	// when stopping the agent itself (it forwards SIGTERM to all
	// children first).
	ShutdownGraceSeconds int `yaml:"shutdown_grace_seconds"`

	// BinaryPaths maps tool name → filesystem path of the upstream
	// binary. Missing keys fail Submit with ErrUpstreamCrash.
	BinaryPaths map[string]string `yaml:"binary_paths"`

	// UpstreamVersionPinned maps tool name → pinned version string.
	// Surfaced via /v1/version for diagnostics. Does NOT affect which
	// binary is executed (the agent uses whatever binary_paths points
	// at); it is purely informational.
	UpstreamVersionPinned map[string]string `yaml:"upstream_version_pinned"`

	// HttpAPIExtras is for upstream binaries that ship an HTTP API
	// wrapper (e.g. redis-shake-http-api on :9320). The agent can
	// auto-fork the wrapper alongside the main process to scrape
	// /metrics instead of parsing stdout.
	HttpAPIExtras map[string]HttpAPIExtra `yaml:"http_api_extras"`
}

// HttpAPIExtra configures an optional sidecar HTTP wrapper the agent
// can spawn alongside the upstream tool to scrape metrics.
type HttpAPIExtra struct {
	// Binary is the path to the wrapper (e.g. /usr/local/bin/redis-shake-http-api).
	Binary string `yaml:"binary"`
	// PortBase is the start of the per-task port allocation pool
	// (the agent increments per task). The wrapper's --http-port flag
	// is set to this value + task index.
	PortBase int `yaml:"port_base"`
	// ExtraArgs are passed verbatim after the binary name and before
	// the upstream tool's config. Used to wire --http-port.
	ExtraArgs []string `yaml:"extra_args"`
}

// Load reads, parses, and validates a YAML config file.
func Load(path string) (Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var s Settings
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return s.Normalize(), s.Validate()
}

// Normalize fills in defaults so callers can assume non-zero values.
func (s Settings) Normalize() Settings {
	if s.BindAddr == "" {
		s.BindAddr = "127.0.0.1:9010"
	}
	if s.WorkRoot == "" {
		s.WorkRoot = filepath.Join(os.TempDir(), "redis-shake-agent", "works")
	}
	if s.LogRoot == "" {
		s.LogRoot = filepath.Join(os.TempDir(), "redis-shake-agent", "logs")
	}
	if s.ShutdownGraceSeconds <= 0 {
		s.ShutdownGraceSeconds = 30
	}
	if s.MaxConcurrentTasks <= 0 {
		s.MaxConcurrentTasks = 16
	}
	if s.BinaryPaths == nil {
		s.BinaryPaths = map[string]string{}
	}
	if s.UpstreamVersionPinned == nil {
		s.UpstreamVersionPinned = map[string]string{}
	}
	if s.HttpAPIExtras == nil {
		s.HttpAPIExtras = map[string]HttpAPIExtra{}
	}
	return s
}

// Validate rejects configs that would brick the agent at startup.
// Failures here are surfaced as a fatal log on serve, not as a
// per-request error.
func (s Settings) Validate() error {
	if _, err := time.ParseDuration("0s"); err != nil {
		// placeholder so the time import is "used" even when no
		// duration fields ship yet (grace_seconds is int seconds).
		_ = err
	}
	if s.BindAddr == "" {
		return fmt.Errorf("bind_addr is required")
	}
	return nil
}
