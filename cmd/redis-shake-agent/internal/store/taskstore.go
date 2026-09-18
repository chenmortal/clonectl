// Package store holds the in-memory task table.
//
// Persistence is deliberately absent: the agent's contract is that
// clonectl owns the source-of-truth for task lifecycle (DB rows in
// SyncRun / CheckRun). The agent is a stateless executor that
// reconciles its in-memory table to whatever the upstream processes
// are doing at the moment — and forgets them after LogRetentionDays.
//
// This means: if the agent restarts mid-run, the orphaned upstream
// processes keep running (their stdout log keeps being appended
// under the original work_dir), and clonectl learns about them via
// /v1/tasks/list on its next reconciliation pass. We do NOT try to
// re-attach to processes across restarts; clonectl decides what to
// do with the orphaned run.
package store

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Tool is the upstream family key in the task store.
type Tool string

const (
	ToolShake     Tool = "redis-shake"
	ToolFullCheck Tool = "redis-fullcheck"
)

// Task is the agent-side view of one running or finished task.
type Task struct {
	// Identity
	TaskID string
	Tool   Tool
	Mode   string

	// Lifecycle
	StartedAt time.Time
	EndedAt   time.Time

	// Process handle (nil only after the process has been reaped).
	Process *Process

	// WorkDir is where the agent wrote the per-task TOML / argv /
	// result.db / captured logs.
	WorkDir string

	// ResultDBPath is set for FullCheck tasks; empty for Shake.
	ResultDBPath string

	// CleanupAt is the deadline after which the store GC may remove
	// this task from memory and the work_dir on disk.
	CleanupAt time.Time
}

// State returns a coarse lifecycle label derived from the Process
// pointer and timestamps. Used by handlers that don't need the full
// Process introspection.
func (t *Task) State() string {
	switch {
	case t.Process != nil && t.Process.IsRunning():
		return "running"
	case t.EndedAt.IsZero():
		return "queued"
	default:
		if t.Process != nil && t.Process.ExitCode() == 0 {
			return "success"
		}
		return "failed"
	}
}

// TaskStore is the in-memory map keyed by TaskID.
type TaskStore struct {
	mu     sync.RWMutex
	tasks  map[string]*Task
}

// NewTaskStore returns an empty store.
func NewTaskStore() *TaskStore {
	return &TaskStore{tasks: make(map[string]*Task)}
}

// ErrNotFound is returned by Get / Remove when the task is unknown.
var ErrNotFound = errors.New("task not found")

// Put inserts or replaces a task.
func (s *TaskStore) Put(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.TaskID] = t
}

// Get returns a copy-safe snapshot pointer; callers MUST NOT mutate
// the returned Task's fields without holding the store lock.
func (s *TaskStore) Get(taskID string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

// Remove deletes the entry. Returns ErrNotFound if missing.
func (s *TaskStore) Remove(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[taskID]; !ok {
		return ErrNotFound
	}
	delete(s.tasks, taskID)
	return nil
}

// List returns lightweight summaries; the Process pointer is omitted
// to keep callers from racing the runner.
func (s *TaskStore) List(filterTool Tool) []Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Summary, 0, len(s.tasks))
	for _, t := range s.tasks {
		if filterTool != "" && t.Tool != filterTool {
			continue
		}
		out = append(out, Summary{
			TaskID:    t.TaskID,
			Tool:      string(t.Tool),
			Mode:      t.Mode,
			State:     t.State(),
			StartedAt: t.StartedAt,
		})
	}
	return out
}

// Summary is the list-row projection.
type Summary struct {
	TaskID    string    `json:"task_id"`
	Tool      string    `json:"tool"`
	Mode      string    `json:"mode"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
}

// Sweep removes tasks whose CleanupAt has passed and asks their
// underlying Process to exit if still running. Returns the IDs that
// were removed (useful for retention-loop logging).
func (s *TaskStore) Sweep(ctx context.Context, now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed []string
	for id, t := range s.tasks {
		if now.Before(t.CleanupAt) {
			continue
		}
		if t.Process != nil && t.Process.IsRunning() {
			_ = t.Process.Stop(ctx)
		}
		delete(s.tasks, id)
		removed = append(removed, id)
	}
	return removed
}
