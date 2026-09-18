// Package runner — isolation: per-task working directories, ports,
// and result.db paths.
//
// The agent multiplexes many concurrent tasks on a single host. Each
// task needs its own:
//
//   - Working directory (shake.toml + captured logs + result.db).
//   - HTTP API wrapper port (when the upstream ships one, e.g.
//     redis-shake-http-api at :9320+N), so metrics don't collide.
//   - Log file (the Process.logFile).
//
// Allocations are derived from the task_id (UUID-shaped string from
// clonectl) and a monotonically-increasing port counter. Working
// directories live under cfg.WorkRoot and are garbage-collected by
// the store's retention loop.
package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Isolation owns the per-task resource allocator.
type Isolation struct {
	workRoot string
	nextPort atomic.Int32
}

// NewIsolation builds an Isolation with portBase as the starting
// allocation number for the sidecar HTTP API wrapper.
func NewIsolation(workRoot string, portBase int) *Isolation {
	iso := &Isolation{workRoot: workRoot}
	if portBase > 0 {
		iso.nextPort.Store(int32(portBase))
	}
	return iso
}

// AllocateWorkDir creates and returns a unique per-task working
// directory. It is safe to call concurrently; the underlying
// MkdirAll is idempotent.
func (i *Isolation) AllocateWorkDir(taskID string) (string, error) {
	if !safeIdentifier(taskID) {
		return "", fmt.Errorf("isolation: unsafe task_id %q", taskID)
	}
	dir := filepath.Join(i.workRoot, taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("isolation: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// AllocatePort returns the next free port in the pool. The atomic
// guarantees monotonic uniqueness across concurrent AllocateWorkDir
// calls in the same process.
func (i *Isolation) AllocatePort() int {
	return int(i.nextPort.Add(1))
}

// safeIdentifier rejects path-traversal attempts in the task_id.
// We treat the task_id as opaque (clonectl generates it) but enforce
// a conservative allowlist as defense in depth.
func safeIdentifier(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	if strings.ContainsAny(s, "/\\") || strings.Contains(s, "..") {
		return false
	}
	return true
}
