// Package scheduler hosts ALL periodic work on one gocron scheduler:
// user sync/check tasks, the run pollers and the HA election ticks.
// Monitoring (job run state) is built on gocron event listeners.
package scheduler

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Execution is one observed job run (bounded history per job).
type Execution struct {
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	Error      string    `json:"error"`
}

// JobState is the monitor's per-job record. Kept in memory only — restarts
// reset history (the API notes this; gocron's own Job API holds schedule facts).
type JobState struct {
	LastStart        time.Time
	LastEnd          time.Time
	LastError        string
	Running          bool
	RunCount         int
	FailCount        int
	ConsecutiveFails int
	Executions       []Execution // ring of last 10
}

// Monitor records job executions fed by gocron event listeners.
type Monitor struct {
	mu   sync.Mutex
	jobs map[uuid.UUID]*JobState
}

// NewMonitor builds an empty monitor.
func NewMonitor() *Monitor {
	return &Monitor{jobs: map[uuid.UUID]*JobState{}}
}

func (m *Monitor) state(id uuid.UUID) *JobState {
	s, ok := m.jobs[id]
	if !ok {
		s = &JobState{}
		m.jobs[id] = s
	}
	return s
}

// BeforeJob marks the start of an execution.
func (m *Monitor) BeforeJob(jobID uuid.UUID, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state(jobID)
	s.LastStart = time.Now()
	s.Running = true
}

// AfterJob marks a successful completion.
func (m *Monitor) AfterJob(jobID uuid.UUID, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state(jobID)
	s.Running = false
	s.LastEnd = time.Now()
	s.RunCount++
	s.ConsecutiveFails = 0
	s.push(Execution{
		StartedAt:  s.LastStart,
		DurationMs: s.LastEnd.Sub(s.LastStart).Milliseconds(),
	})
}

// AfterJobError marks a failed completion.
func (m *Monitor) AfterJobError(jobID uuid.UUID, _ string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state(jobID)
	s.Running = false
	s.LastEnd = time.Now()
	s.RunCount++
	s.FailCount++
	s.ConsecutiveFails++
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	s.LastError = msg
	s.push(Execution{StartedAt: s.LastStart, DurationMs: s.LastEnd.Sub(s.LastStart).Milliseconds(), Error: msg})
}

func (s *JobState) push(e Execution) {
	const keep = 10
	s.Executions = append(s.Executions, e)
	if len(s.Executions) > keep {
		s.Executions = s.Executions[len(s.Executions)-keep:]
	}
}

// State returns a copy of the recorded state (zero value when unseen).
func (m *Monitor) State(jobID uuid.UUID) JobState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.jobs[jobID]; ok {
		cp := *s
		cp.Executions = append([]Execution(nil), s.Executions...)
		return cp
	}
	return JobState{}
}
