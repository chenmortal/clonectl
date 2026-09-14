package scheduler

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
	"rclone_sync/internal/services"
)

// TaskExec runs one sync task (wired to services.RunTask with schedule trigger).
type TaskExec func(taskID int64)

// CheckExec runs one check task.
type CheckExec func(checkID int64)

// Service owns the single gocron scheduler and the leader gate.
type Service struct {
	sched     gocron.Scheduler
	db        *gorm.DB
	rc        *rclone.Client
	cfg       config.Settings
	mon       *Monitor
	execTask  TaskExec
	execCheck CheckExec

	heartbeatFn func() // cluster hooks, optional
	electFn     func()

	mu               sync.RWMutex
	leader           bool
	schedules        map[string]string // job name → human schedule string
	schedulerStarted bool
}

// New builds the service (not yet started). rc drives the run-poll jobs.
func New(db *gorm.DB, rc *rclone.Client, cfg config.Settings, execTask TaskExec, execCheck CheckExec) (*Service, error) {
	mon := NewMonitor()
	sched, err := gocron.NewScheduler(
		gocron.WithLocation(time.UTC),
		gocron.WithGlobalJobOptions(
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
			gocron.WithEventListeners(
				gocron.BeforeJobRuns(mon.BeforeJob),
				gocron.AfterJobRuns(mon.AfterJob),
				gocron.AfterJobRunsWithError(mon.AfterJobError),
			),
		),
	)
	if err != nil {
		return nil, err
	}
	return &Service{
		sched:     sched,
		db:        db,
		rc:        rc,
		cfg:       cfg,
		mon:       mon,
		execTask:  execTask,
		execCheck: execCheck,
		schedules: map[string]string{},
	}, nil
}

// WithClusterHooks wires the HA elector ticks into this scheduler. Optional:
// single-node deployments pass nothing.
func (s *Service) WithClusterHooks(heartbeat, elect func()) *Service {
	s.heartbeatFn = heartbeat
	s.electFn = elect
	return s
}

// --- leader gate ---

// SetLeader flips the gate. On gain: rebuild user jobs from the DB (picking
// up writes made while standby). On loss: remove user jobs — running ones
// finish naturally; no new rclone runs start on this node.
func (s *Service) SetLeader(value bool) {
	s.mu.Lock()
	if value == s.leader {
		s.mu.Unlock()
		return
	}
	s.leader = value
	s.mu.Unlock()

	if value {
		if err := s.SyncFromDB(); err != nil {
			slog.Error("scheduler: sync from db failed", "err", err)
		}
		return
	}
	s.removeUserJobs()
}

func (s *Service) isLeader() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leader
}

// --- cron validation (parity with Python parse_cron: exactly 5 fields) ---

// ValidateCron accepts standard 5-field crontab expressions only.
func ValidateCron(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have 5 fields: %q", expr)
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return nil
}

// --- job identity helpers ---

func taskJobName(id int64) string  { return fmt.Sprintf("task-%d", id) }
func checkJobName(id int64) string { return fmt.Sprintf("checktask-%d", id) }
func taskTag(id int64) string      { return fmt.Sprintf("task-%d", id) }
func checkTag(id int64) string     { return fmt.Sprintf("checktask-%d", id) }

func isUserJobName(name string) bool {
	return strings.HasPrefix(name, "task-") || strings.HasPrefix(name, "checktask-")
}

func (s *Service) setSchedule(name, desc string) {
	s.mu.Lock()
	s.schedules[name] = desc
	s.mu.Unlock()
}

func (s *Service) scheduleOf(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.schedules[name]; ok {
		return v
	}
	return ""
}

// --- registration (leader-gated) ---

// RegisterTask (re-)registers one sync task's cron job. No-op on standby.
func (s *Service) RegisterTask(t *database.SyncTask) error {
	if !s.isLeader() {
		return nil
	}
	if err := ValidateCron(t.Cron); err != nil {
		return err
	}
	name := taskJobName(t.ID)
	s.sched.RemoveByTags(taskTag(t.ID))
	_, err := s.newJob(
		name, []string{"user", "sync", taskTag(t.ID)},
		gocron.CronJob(t.Cron, false),
		func() { s.execTask(t.ID) },
	)
	if err == nil {
		s.setSchedule(name, t.Cron)
	}
	return err
}

// RegisterCheckTask (re-)registers one check task; nil cron removes instead
// (manual/pre-check-only tasks carry no schedule).
func (s *Service) RegisterCheckTask(t *database.CheckTask) error {
	if !s.isLeader() {
		return nil
	}
	if t.Cron == nil || *t.Cron == "" {
		s.RemoveCheckTask(t.ID)
		return nil
	}
	if err := ValidateCron(*t.Cron); err != nil {
		return err
	}
	name := checkJobName(t.ID)
	s.sched.RemoveByTags(checkTag(t.ID))
	_, err := s.newJob(
		name, []string{"user", "check", checkTag(t.ID)},
		gocron.CronJob(*t.Cron, false),
		func() { s.execCheck(t.ID) },
	)
	if err == nil {
		s.setSchedule(name, *t.Cron)
	}
	return err
}

// RemoveTask drops a sync task's job. No-op on standby.
func (s *Service) RemoveTask(id int64) {
	if !s.isLeader() {
		return
	}
	s.sched.RemoveByTags(taskTag(id))
}

// RemoveCheckTask drops a check task's job. No-op on standby.
func (s *Service) RemoveCheckTask(id int64) {
	if !s.isLeader() {
		return
	}
	s.sched.RemoveByTags(checkTag(id))
}

func (s *Service) newJob(name string, tags []string, def gocron.JobDefinition, fn func()) (string, error) {
	j, err := s.sched.NewJob(
		def,
		gocron.NewTask(fn),
		gocron.WithName(name),
		gocron.WithTags(tags...),
	)
	if err != nil {
		return "", err
	}
	return j.ID().String(), nil
}

// --- sync from DB ---

// SyncFromDB reconciles registered user jobs with enabled tasks in the DB.
func (s *Service) SyncFromDB() error {
	var tasks []database.SyncTask
	if err := s.db.Find(&tasks).Error; err != nil {
		return err
	}
	var checks []database.CheckTask
	if err := s.db.Find(&checks).Error; err != nil {
		return err
	}

	enabled := map[string]bool{}
	for i := range tasks {
		t := &tasks[i]
		if t.Enabled {
			if err := s.RegisterTask(t); err != nil {
				slog.Error("scheduler: register task failed", "id", t.ID, "err", err)
				continue
			}
			enabled[taskJobName(t.ID)] = true
		}
	}
	for i := range checks {
		t := &checks[i]
		if t.Enabled && t.Cron != nil && *t.Cron != "" {
			if err := s.RegisterCheckTask(t); err != nil {
				slog.Error("scheduler: register check task failed", "id", t.ID, "err", err)
				continue
			}
			enabled[checkJobName(t.ID)] = true
		}
	}

	// Remove user jobs that are no longer enabled.
	for _, j := range s.sched.Jobs() {
		name := j.Name()
		if isUserJobName(name) && !enabled[name] {
			_ = s.sched.RemoveJob(j.ID())
		}
	}
	return nil
}

func (s *Service) removeUserJobs() {
	for _, j := range s.sched.Jobs() {
		if isUserJobName(j.Name()) {
			_ = s.sched.RemoveJob(j.ID())
		}
	}
}

// --- lifecycle ---

// Start starts the scheduler and registers the internal poll/cluster jobs.
func (s *Service) Start() error {
	if _, err := s.newJob("poll-running-runs", []string{"internal", "poll"},
		gocron.DurationJob(time.Duration(s.cfg.PollInterval)*time.Second),
		func() { pollRunningRuns(s.db, s.rc) }); err != nil {
		return err
	}
	s.setSchedule("poll-running-runs", fmt.Sprintf("every %ds", s.cfg.PollInterval))
	if _, err := s.newJob("poll-running-checks", []string{"internal", "poll"},
		gocron.DurationJob(time.Duration(s.cfg.PollInterval)*time.Second),
		func() { pollRunningChecks(s.db, s.rc) }); err != nil {
		return err
	}
	s.setSchedule("poll-running-checks", fmt.Sprintf("every %ds", s.cfg.PollInterval))

	if s.heartbeatFn != nil {
		if _, err := s.newJob("cluster-heartbeat", []string{"internal", "cluster"},
			gocron.DurationJob(time.Duration(s.cfg.HeartbeatInterval)*time.Second),
			s.heartbeatFn); err != nil {
			return err
		}
		s.setSchedule("cluster-heartbeat", fmt.Sprintf("every %ds", s.cfg.HeartbeatInterval))
	}
	if s.electFn != nil {
		if _, err := s.newJob("cluster-elect", []string{"internal", "cluster"},
			gocron.DurationJob(30*time.Second), s.electFn); err != nil {
			return err
		}
		s.setSchedule("cluster-elect", "every 30s")
	}

	s.sched.Start()
	s.mu.Lock()
	s.schedulerStarted = true
	s.mu.Unlock()
	return nil
}

// Shutdown stops the scheduler (running jobs finish).
func (s *Service) Shutdown() error {
	return s.sched.Shutdown()
}

// Poll wiring — the poll jobs call into the services package.
func pollRunningRuns(db *gorm.DB, rc *rclone.Client)   { services.PollRunningRuns(db, rc) }
func pollRunningChecks(db *gorm.DB, rc *rclone.Client) { services.PollRunningChecks(db, rc) }

// --- monitor views ---

// TaskIDKind identifies a job's business payload.
type TaskIDKind struct {
	Kind   string // "task" | "check" | "internal"
	TaskID *int64
}

func kindFromName(name string) TaskIDKind {
	if id, ok := trimID(name, "task-"); ok {
		return TaskIDKind{Kind: "task", TaskID: &id}
	}
	if id, ok := trimID(name, "checktask-"); ok {
		return TaskIDKind{Kind: "check", TaskID: &id}
	}
	return TaskIDKind{Kind: "internal"}
}

func trimID(name, prefix string) (int64, bool) {
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(name, prefix), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// JobView merges gocron schedule facts with monitor state for the API.
type JobView struct {
	ID                 string
	Name               string
	Kind               string
	TaskID             *int64
	Tags               []string
	Schedule           string
	NextRun            *time.Time
	NextRuns           []time.Time
	LastRunStartedAt   *time.Time
	LastRunCompletedAt *time.Time
	IsRunning          bool
	LastError          string
	RunCount           int
	FailCount          int
	ConsecutiveFails   int
	Executions         []Execution
}

// Snapshot lists all jobs with their monitor state.
func (s *Service) Snapshot() []JobView {
	out := make([]JobView, 0)
	for _, j := range s.sched.Jobs() {
		v := JobView{
			ID:        j.ID().String(),
			Name:      j.Name(),
			Tags:      j.Tags(),
			Schedule:  s.scheduleOf(j.Name()),
			IsRunning: isRunning(j),
		}
		k := kindFromName(j.Name())
		v.Kind = k.Kind
		v.TaskID = k.TaskID

		if t, err := j.NextRun(); err == nil {
			v.NextRun = &t
		}
		if runs, err := j.NextRuns(5); err == nil {
			v.NextRuns = runs
		}
		if t, err := j.LastRunStartedAt(); err == nil {
			v.LastRunStartedAt = &t
		}
		if t, err := j.LastRunCompletedAt(); err == nil {
			v.LastRunCompletedAt = &t
		}

		st := s.mon.State(j.ID())
		v.LastError = st.LastError
		v.RunCount = st.RunCount
		v.FailCount = st.FailCount
		v.ConsecutiveFails = st.ConsecutiveFails
		v.Executions = st.Executions
		if v.IsRunning {
			v.LastRunStartedAt = &st.LastStart
		}
		out = append(out, v)
	}
	return out
}

func isRunning(j gocron.Job) bool {
	b, err := j.IsRunning()
	return err == nil && b
}

// JobByID finds one job view by gocron job id.
func (s *Service) JobByID(id string) (JobView, bool) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return JobView{}, false
	}
	for _, v := range s.Snapshot() {
		if v.ID == uid.String() {
			return v, true
		}
	}
	return JobView{}, false
}

// RunUserJobNow manually triggers a registered user job's payload through
// the service layer (NOT job.RunNow — callers need the run row and the
// concurrency guard; see the plan).
func (s *Service) RunUserJobNow(jobID string) (TaskIDKind, bool) {
	for _, j := range s.sched.Jobs() {
		if j.ID().String() != jobID {
			continue
		}
		k := kindFromName(j.Name())
		if k.Kind == "internal" || k.TaskID == nil {
			return k, false
		}
		if k.Kind == "task" {
			s.execTask(*k.TaskID)
		} else {
			s.execCheck(*k.TaskID)
		}
		return k, true
	}
	return TaskIDKind{}, false
}
