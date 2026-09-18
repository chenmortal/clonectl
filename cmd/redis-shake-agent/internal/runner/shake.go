// Package runner — shake.go: turn a Spec into a running redis-shake
// child process and its per-task work_dir / log file.
//
// The split between tomlgen.go (TOML rendering) and shake.go (process
// orchestration) is intentional: when the upstream schema changes,
// tomlgen is the only file to edit. shake.go stays focused on
// fork/exec/cleanup.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/chenmortal/redis-shake-agent/internal/redact"
	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// ShakeRequest is the agent-side projection of the clonectl wire
// shape. We accept the request as raw JSON so the agent's view of
// "what clonectl sent" is decoupled from any upstream changes — the
// parser below is the single seam.
type ShakeRequest struct {
	Tool   string          `json:"tool"`
	TaskID string          `json:"task_id"`
	Mode   string          `json:"mode"`
	Config json.RawMessage `json:"config"`
}

// SubmitShake parses req, renders the TOML, launches redis-shake, and
// returns the registered Task. It returns an error before any
// external side effect (no fork, no file write) when validation
// fails.
func SubmitShake(
	ctx context.Context,
	cfg ShakeConfig,
	iso *Isolation,
	tasks *store.TaskStore,
	req ShakeRequest,
	secrets ShakeSecrets,
) (*store.Task, error) {
	if req.TaskID == "" {
		return nil, errors.New("shake: task_id required")
	}
	if req.Mode == "" {
		return nil, errors.New("shake: mode required")
	}

	// Parse the upstream-specific bits.
	spec, err := parseShakeSpec(req.Mode, req.Config)
	if err != nil {
		return nil, err
	}
	if secrets.SourcePassword != "" {
		spec.Reader.Password = secrets.SourcePassword
	}
	if secrets.TargetPassword != "" {
		spec.Writer.Password = secrets.TargetPassword
	}

	workDir, err := iso.AllocateWorkDir(req.TaskID)
	if err != nil {
		return nil, err
	}

	confPath := filepath.Join(workDir, "shake.toml")
	if err := RenderTOML(spec, confPath); err != nil {
		return nil, fmt.Errorf("shake: render toml: %w", err)
	}

	binPath := cfg.Binary
	if binPath == "" {
		return nil, errors.New("shake: redis-shake binary not configured")
	}

	argv := ArgvForShake(confPath)
	env := []string{
		"SOURCE_PASSWORD=" + spec.Reader.Password,
		"TARGET_PASSWORD=" + spec.Writer.Password,
	}

	proc, err := store.Start(store.Options{
		Path:    binPath,
		Args:    argv,
		WorkDir: workDir,
		Env:     redactEnv(env),
	})
	if err != nil {
		return nil, fmt.Errorf("shake: start: %w", err)
	}

	t := &store.Task{
		TaskID:    req.TaskID,
		Tool:      store.ToolShake,
		Mode:      req.Mode,
		StartedAt: time.Now(),
		Process:   proc,
		WorkDir:   workDir,
		CleanupAt: time.Now().Add(time.Duration(cfg.LogRetentionDays) * 24 * time.Hour),
	}
	tasks.Put(t)
	return t, nil
}

// ShakeConfig is the subset of agent Settings the shake runner needs.
// Defined as a struct (not the full Settings) so callers can swap in
// a stub in tests.
type ShakeConfig struct {
	Binary           string
	LogRetentionDays int
}

// parseShakeSpec decodes the clonectl-supplied Config blob into the
// agent's ShakeSpec view. The mapping here is the only place we
// translate the tool-agnostic Spec → upstream-specific knobs.
func parseShakeSpec(mode string, raw json.RawMessage) (ShakeSpec, error) {
	var s ShakeSpec
	switch mode {
	case "sync_reader", "rdb_reader", "scan_reader":
		s.Reader.Mode = mode
	default:
		return s, fmt.Errorf("shake: unsupported mode %q (supported: sync_reader / rdb_reader / scan_reader)", mode)
	}

	// Expect either:
	//   { "source": {...}, "target": {...}, "extra": {...} }
	// or just the Spec source/target shape from clonectl.
	var payload struct {
		Source ShakeConn   `json:"source"`
		Target ShakeWriter `json:"target"`
		Extra  struct {
			Snapshot       bool  `json:"snapshot"`
			Parallel       int   `json:"parallel"`
			RDBPath        string `json:"rdb_path"`
			ScanCount      int   `json:"scan_count"`
			KeysPerRequest int64 `json:"keys_per_request"`
			Filter         ShakeFilter `json:"filter"`
			Advanced       ShakeAdvanced `json:"advanced"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return s, fmt.Errorf("shake: decode config: %w", err)
	}

	s.Reader.Address = payload.Source.Address
	s.Reader.Username = payload.Source.Username
	s.Reader.Sync.Snapshot = payload.Extra.Snapshot
	s.Reader.Sync.Parallel = payload.Extra.Parallel
	s.Reader.RDB.Path = payload.Extra.RDBPath
	s.Reader.Scan.Count = payload.Extra.ScanCount
	s.Reader.Scan.KeysPerRequest = payload.Extra.KeysPerRequest

	s.Writer = payload.Target
	if s.Writer.Mode == "" {
		s.Writer.Mode = "redis_writer"
	}

	s.Filter = payload.Extra.Filter
	s.Advanced = payload.Extra.Advanced
	return s, nil
}

// ShakeConn mirrors the clonectl endpoint shape for a single
// connection (host:port + username).
type ShakeConn struct {
	Address  string `json:"address"`
	Username string `json:"username"`
}

// redactEnv scrubs any kv-shaped password before passing env vars to
// the child. Belt-and-braces; the values themselves are passed via
// process env not argv so they should already be safe.
func redactEnv(env []string) []string {
	out := make([]string, len(env))
	for i, e := range env {
		out[i] = redact.Redact(e)
	}
	return out
}

// ReadShakeLog returns the tail of the task's stdout log starting at
// offset. Pass limit=0 to read to EOF.
func ReadShakeLog(t *store.Task, offset int64, limit int) ([]byte, int64, bool, error) {
	if t.Process == nil {
		return nil, offset, true, nil
	}
	return t.Process.ReadLogs(offset, limit)
}

// StopShake sends SIGTERM and waits for the child. Cancellation of
// ctx escalates to SIGKILL immediately.
func StopShake(ctx context.Context, t *store.Task) error {
	if t.Process == nil {
		return nil
	}
	return t.Process.Stop(ctx)
}
