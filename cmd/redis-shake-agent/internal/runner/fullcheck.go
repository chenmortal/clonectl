// Package runner — fullcheck.go: turn a Spec into a running
// redis-full-check child process.
//
// Compared to redis-shake this is simpler:
//   - No TOML: upstream is CLI-flag driven (see argvgen.go).
//   - The result.db SQLite file is left on disk; we expose a stub
//     summary via /v1/tasks/status and the diff.txt via /v1/tasks/logs
//     until we ship a sqlite reader (TODO).
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// FullCheckRequest is the agent-side projection for the FullCheck
// wire shape, mirroring ShakeRequest.
type FullCheckRequest struct {
	Tool   string          `json:"tool"`
	TaskID string          `json:"task_id"`
	Mode   string          `json:"mode"` // compare mode as string "1".."4"
	Config json.RawMessage `json:"config"`
}

// SubmitFullCheck parses req, builds the argv, launches the child,
// and returns the registered Task.
func SubmitFullCheck(
	ctx context.Context,
	cfg FullCheckConfig,
	iso *Isolation,
	tasks *store.TaskStore,
	req FullCheckRequest,
	secrets FullCheckSecrets,
) (*store.Task, error) {
	if req.TaskID == "" {
		return nil, errors.New("fullcheck: task_id required")
	}
	if req.Mode == "" {
		return nil, errors.New("fullcheck: mode required")
	}

	spec, err := parseFullCheckSpec(req.Mode, req.Config)
	if err != nil {
		return nil, err
	}
	if secrets.SourcePassword != "" {
		spec.SourcePassword = secrets.SourcePassword
	}
	if secrets.TargetPassword != "" {
		spec.TargetPassword = secrets.TargetPassword
	}

	workDir, err := iso.AllocateWorkDir(req.TaskID)
	if err != nil {
		return nil, err
	}

	argv, err := ArgvForFullCheck(spec, workDir)
	if err != nil {
		return nil, fmt.Errorf("fullcheck: argv: %w", err)
	}

	binPath := cfg.Binary
	if binPath == "" {
		return nil, errors.New("fullcheck: redis-full-check binary not configured")
	}

	proc, err := store.Start(store.Options{
		Path:    binPath,
		Args:    argv,
		WorkDir: workDir,
	})
	if err != nil {
		return nil, fmt.Errorf("fullcheck: start: %w", err)
	}

	t := &store.Task{
		TaskID:       req.TaskID,
		Tool:         store.ToolFullCheck,
		Mode:         req.Mode,
		StartedAt:    time.Now(),
		Process:      proc,
		WorkDir:      workDir,
		ResultDBPath: filepath.Join(workDir, "result.db"),
		CleanupAt:    time.Now().Add(time.Duration(cfg.LogRetentionDays) * 24 * time.Hour),
	}
	tasks.Put(t)
	return t, nil
}

// FullCheckConfig is the subset of agent Settings the FullCheck
// runner needs.
type FullCheckConfig struct {
	Binary           string
	LogRetentionDays int
}

func parseFullCheckSpec(mode string, raw json.RawMessage) (FullCheckSpec, error) {
	var s FullCheckSpec
	switch mode {
	case "1", "2", "3", "4":
		fmt.Sscanf(mode, "%d", &s.CompareMode)
	default:
		return s, fmt.Errorf("fullcheck: compare_mode %q out of range [1..4]", mode)
	}

	var payload struct {
		Source         ShakeConn `json:"source"`
		Target         ShakeConn `json:"target"`
		CompareTimes   int       `json:"compare_times"`
		QPS            int       `json:"qps"`
		Interval       int       `json:"interval_seconds"`
		BatchCount     int       `json:"batch_count"`
		Parallel       int       `json:"parallel"`
		BigKeyThresh   int64     `json:"big_key_threshold"`
		FilterList     string    `json:"filter_list"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return s, fmt.Errorf("fullcheck: decode config: %w", err)
	}

	s.Source = payload.Source.Address
	s.Target = payload.Target.Address
	s.CompareTimes = payload.CompareTimes
	s.QPS = payload.QPS
	s.IntervalSeconds = payload.Interval
	s.BatchCount = payload.BatchCount
	s.Parallel = payload.Parallel
	s.BigKeyThreshold = payload.BigKeyThresh
	s.FilterList = payload.FilterList
	return s, nil
}
