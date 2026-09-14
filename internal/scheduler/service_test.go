package scheduler

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
)

func schedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}

func newTestService(t *testing.T, db *gorm.DB) (*Service, *[]int64, *[]int64) {
	t.Helper()
	var tasks, checks []int64
	s, err := New(db, nil, config.Settings{PollInterval: 10, HeartbeatInterval: 3},
		func(id int64) { tasks = append(tasks, id) },
		func(id int64) { checks = append(checks, id) })
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Shutdown() })
	return s, &tasks, &checks
}

func mkSyncTask(t *testing.T, db *gorm.DB, name, cron string, enabled bool) *database.SyncTask {
	t.Helper()
	tk := &database.SyncTask{Name: name, SrcDataSourceID: 1, DstDataSourceID: 1,
		SrcPath: "/a", DstPath: "/b", Mode: database.ModeSync, Cron: cron, Enabled: enabled,
		RcloneOptions: database.JSONObject{}}
	require.NoError(t, db.Create(tk).Error)
	return tk
}

func jobNames(s *Service) map[string]bool {
	out := map[string]bool{}
	for _, j := range s.sched.Jobs() {
		out[j.Name()] = true
	}
	return out
}

func TestValidateCron(t *testing.T) {
	assert.NoError(t, ValidateCron("0 3 * * *"))
	assert.NoError(t, ValidateCron("*/5 * * * 1"))
	assert.Error(t, ValidateCron("0 3 * * * *"), "6 fields rejected")
	assert.Error(t, ValidateCron("0 3 * *"), "4 fields rejected")
	assert.Error(t, ValidateCron("bad expr here x y"), "garbage rejected")
	assert.Error(t, ValidateCron("61 3 * * *"), "minute out of range")
}

func TestRegisterAndRemoveTask(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)
	s.SetLeader(true)

	tk := mkSyncTask(t, db, "nightly", "0 3 * * *", true)
	require.NoError(t, s.RegisterTask(tk))
	names := jobNames(s)
	assert.True(t, names["task-"+itoa(int(tk.ID))], "job registered")

	// Re-register with a changed cron replaces the job.
	tk.Cron = "0 4 * * *"
	require.NoError(t, s.RegisterTask(tk))
	assert.Len(t, s.sched.Jobs(), 1, "no duplicate job")
	assert.Equal(t, "0 4 * * *", s.scheduleOf("task-"+itoa(int(tk.ID))))

	// Remove
	s.RemoveTask(tk.ID)
	assert.NotContains(t, jobNames(s), "task-"+itoa(int(tk.ID)))
}

func TestStandbyNeverRegisters(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)
	// leader=false

	tk := mkSyncTask(t, db, "t", "0 3 * * *", true)
	require.NoError(t, s.RegisterTask(tk))
	s.RemoveTask(tk.ID)
	assert.Empty(t, s.sched.Jobs(), "no-ops while standby")
}

func TestRegisterCheckTaskCronNullable(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)
	s.SetLeader(true)

	manual := &database.CheckTask{Name: "manual", SrcDataSourceID: 1, DstDataSourceID: 1,
		SrcPath: "/a", DstPath: "/b", Enabled: true, CheckOptions: database.JSONObject{}}
	require.NoError(t, db.Create(manual).Error)
	require.NoError(t, s.RegisterCheckTask(manual))
	assert.NotContains(t, jobNames(s), "checktask-"+itoa(int(manual.ID)), "nil cron → no job")

	scheduled := &database.CheckTask{Name: "hourly", SrcDataSourceID: 1, DstDataSourceID: 1,
		SrcPath: "/a", DstPath: "/b", Enabled: true, CheckOptions: database.JSONObject{}}
	cron := "0 * * * *"
	scheduled.Cron = &cron
	require.NoError(t, db.Create(scheduled).Error)
	require.NoError(t, s.RegisterCheckTask(scheduled))
	assert.Contains(t, jobNames(s), "checktask-"+itoa(int(scheduled.ID)))

	// Clearing the cron removes the job.
	scheduled.Cron = nil
	require.NoError(t, s.RegisterCheckTask(scheduled))
	assert.NotContains(t, jobNames(s), "checktask-"+itoa(int(scheduled.ID)))
}

func TestSyncFromDBReconciles(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)
	s.SetLeader(true)

	on := mkSyncTask(t, db, "on", "0 3 * * *", true)
	off := mkSyncTask(t, db, "off", "0 4 * * *", false)
	_ = off
	require.NoError(t, s.SyncFromDB())

	names := jobNames(s)
	assert.Contains(t, names, "task-"+itoa(int(on.ID)))
	assert.NotContains(t, names, "task-"+itoa(int(off.ID)))

	// Register a stale job directly, then re-sync: it must be removed.
	stale := mkSyncTask(t, db, "stale", "0 5 * * *", true)
	require.NoError(t, s.RegisterTask(stale))
	require.NoError(t, db.Model(stale).Update("enabled", false).Error)
	require.NoError(t, s.SyncFromDB())
	assert.NotContains(t, jobNames(s), "task-"+itoa(int(stale.ID)))
}

func TestSetLeaderGainsAndLoses(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)

	tk := mkSyncTask(t, db, "t", "0 3 * * *", true)

	// Standby: nothing registered even after SetLeader(false)→(no-op).
	s.SetLeader(false)
	assert.Empty(t, s.sched.Jobs())

	// Gain: syncs from DB (picks up rows written while standby).
	s.SetLeader(true)
	assert.Contains(t, jobNames(s), "task-"+itoa(int(tk.ID)))

	// Lose: user jobs removed, internal jobs survive.
	require.NoError(t, s.Start())
	s.SetLeader(false)
	names := jobNames(s)
	assert.NotContains(t, names, "task-"+itoa(int(tk.ID)))
	assert.Contains(t, names, "poll-running-runs", "internal jobs stay")

	// Regain: rebuilt from DB.
	s.SetLeader(true)
	assert.Contains(t, jobNames(s), "task-"+itoa(int(tk.ID)))
}

func TestStartRegistersInternalJobs(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)
	s.WithClusterHooks(func() {}, func() {})
	require.NoError(t, s.Start())

	names := jobNames(s)
	assert.Contains(t, names, "poll-running-runs")
	assert.Contains(t, names, "poll-running-checks")
	assert.Contains(t, names, "cluster-heartbeat")
	assert.Contains(t, names, "cluster-elect")
	assert.Equal(t, "every 10s", s.scheduleOf("poll-running-runs"))
}

func TestMonitorTracksJobState(t *testing.T) {
	m := NewMonitor()
	id := mustUUID(t, "3f0c9a6e-1111-2222-3333-444455556666")

	m.BeforeJob(id, "task-1")
	st := m.State(id)
	assert.True(t, st.Running)

	m.AfterJob(id, "task-1")
	st = m.State(id)
	assert.False(t, st.Running)
	assert.Equal(t, 1, st.RunCount)
	assert.Equal(t, 0, st.FailCount)
	assert.Len(t, st.Executions, 1)
	assert.Equal(t, "", st.Executions[0].Error)

	// A failure bumps fail counters and the ring.
	m.BeforeJob(id, "task-1")
	m.AfterJobError(id, "task-1", errors.New("boom"))
	st = m.State(id)
	assert.Equal(t, 2, st.RunCount)
	assert.Equal(t, 1, st.FailCount)
	assert.Equal(t, 1, st.ConsecutiveFails)
	assert.Equal(t, "boom", st.LastError)
	assert.Len(t, st.Executions, 2)
	assert.Equal(t, "boom", st.Executions[1].Error)

	// Unknown job → zero state.
	assert.Equal(t, JobState{}, m.State(mustUUID(t, "99999999-1111-2222-3333-444455556666")))
}

func TestSnapshotMergesGocronAndMonitor(t *testing.T) {
	db := schedDB(t)
	s, _, _ := newTestService(t, db)
	s.SetLeader(true)
	tk := mkSyncTask(t, db, "snap", "0 3 * * *", true)
	require.NoError(t, s.RegisterTask(tk))
	require.NoError(t, s.Start())

	// 1 user task + 2 poll jobs = 3
	snap := s.Snapshot()
	require.Len(t, snap, 3)
	var v *JobView
	for i := range snap {
		if snap[i].Kind == "task" {
			v = &snap[i]
		} else {
			assert.Equal(t, "internal", snap[i].Kind)
			assert.Nil(t, snap[i].TaskID)
		}
	}
	require.NotNil(t, v)
	assert.Equal(t, "task-"+itoa(int(tk.ID)), v.Name)
	assert.Equal(t, "0 3 * * *", v.Schedule)
	assert.NotNil(t, v.NextRun)
	assert.NotEmpty(t, v.NextRuns, "upcoming-runs preview")
	assert.False(t, v.IsRunning)

	// JobByID round-trips
	byID, ok := s.JobByID(v.ID)
	require.True(t, ok)
	assert.Equal(t, v.Name, byID.Name)

	// Internal job classification
	for _, j := range s.Snapshot() {
		if j.Name == "poll-running-runs" {
			assert.Equal(t, "internal", j.Kind)
			assert.Nil(t, j.TaskID)
		}
	}
}

func TestRunUserJobNow(t *testing.T) {
	db := schedDB(t)
	s, tasks, _ := newTestService(t, db)
	s.SetLeader(true)
	tk := mkSyncTask(t, db, "now", "0 3 * * *", true)
	require.NoError(t, s.RegisterTask(tk))
	snap := s.Snapshot()
	require.Len(t, snap, 1)

	k, ok := s.RunUserJobNow(snap[0].ID)
	require.True(t, ok)
	assert.Equal(t, "task", k.Kind)
	require.Len(t, *tasks, 1)
	assert.Equal(t, tk.ID, (*tasks)[0])

	// Unknown id → false
	_, ok = s.RunUserJobNow("not-a-uuid")
	assert.False(t, ok)
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
