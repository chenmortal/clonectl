package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/database"
	"rclone_sync/internal/scheduler"
)

// firstTaskJobID picks the user task job's id out of a jobs list payload.
func firstTaskJobID(t *testing.T, list map[string]any) string {
	t.Helper()
	for _, raw := range list["jobs"].([]any) {
		j := raw.(map[string]any)
		if j["kind"] == "task" {
			return j["id"].(string)
		}
	}
	t.Fatal("no task job in list")
	return ""
}

func schedEnv(t *testing.T) (*Deps, *gin.Engine, string) {
	t.Helper()
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	admin := login(t, r, "root", "pass1234")

	task := database.SyncTask{Name: "watched", SrcDataSourceID: 1, DstDataSourceID: 1,
		SrcPath: "/a", DstPath: "/b", Mode: database.ModeSync, Cron: "0 3 * * *", Enabled: true,
		RcloneOptions: database.JSONObject{}}
	require.NoError(t, d.DB.Create(&task).Error)

	cfg := d.Cfg
	cfg.PollInterval = 10
	cfg.HeartbeatInterval = 3
	s, err := scheduler.New(d.DB, nil, cfg,
		func(id int64) *int64 { return nil },
		func(id int64) *int64 { return nil })
	require.NoError(t, err)
	d.Sched = s
	s.SetLeader(true)
	require.NoError(t, s.SyncFromDB())
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Shutdown() })
	return d, r, admin
}

func TestSchedulerJobsList(t *testing.T) {
	_, r, admin := schedEnv(t)

	w := doJSON(r, http.MethodGet, "/api/scheduler/jobs", admin, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, true, out["is_leader"])

	jobs := out["jobs"].([]any)
	// 1 user task + 2 internal poll jobs; grab the task one.
	var job map[string]any
	userJobs := 0
	for _, raw := range jobs {
		j := raw.(map[string]any)
		if j["kind"] == "task" {
			job = j
			userJobs++
		} else {
			assert.Equal(t, "internal", j["kind"])
			assert.Nil(t, j["task_id"])
		}
	}
	require.NotNil(t, job)
	assert.Equal(t, 1, userJobs)
	assert.Equal(t, "task-1", job["name"])
	assert.EqualValues(t, 1, job["task_id"])
	assert.Equal(t, "0 3 * * *", job["schedule"])
	assert.NotNil(t, job["next_run"])
	assert.NotNil(t, job["tags"])
}

func TestSchedulerJobDetailAndOverview(t *testing.T) {
	_, r, admin := schedEnv(t)

	w := doJSON(r, http.MethodGet, "/api/scheduler/jobs", admin, nil)
	var list map[string]any
	require.NoError(t, unmarshalBody(w, &list))
	jobID := firstTaskJobID(t, list)

	// Detail: upcoming runs + executions ring.
	w = doJSON(r, http.MethodGet, "/api/scheduler/jobs/"+jobID, admin, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var detail map[string]any
	require.NoError(t, unmarshalBody(w, &detail))
	assert.NotEmpty(t, detail["next_runs"], "upcoming-runs preview")
	assert.NotNil(t, detail["executions"])

	// Overview.
	w = doJSON(r, http.MethodGet, "/api/scheduler/overview", admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var ov map[string]any
	require.NoError(t, unmarshalBody(w, &ov))
	assert.Equal(t, true, ov["is_leader"])
	assert.EqualValues(t, 1, ov["user_jobs"])
	assert.EqualValues(t, 0, ov["running_now"])

	// 404 unknown job
	w = doJSON(r, http.MethodGet, "/api/scheduler/jobs/nope", admin, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSchedulerRunNowExecutesService(t *testing.T) {
	var ranID *int64
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	admin := login(t, r, "root", "pass1234")

	task := database.SyncTask{Name: "n", SrcDataSourceID: 1, DstDataSourceID: 1,
		SrcPath: "/a", DstPath: "/b", Mode: database.ModeSync, Cron: "0 3 * * *", Enabled: true,
		RcloneOptions: database.JSONObject{}}
	require.NoError(t, d.DB.Create(&task).Error)

	cfg := d.Cfg
	cfg.PollInterval = 10
	s, err := scheduler.New(d.DB, nil, cfg,
		func(id int64) *int64 { ranID = &id; return nil },
		func(id int64) *int64 { return nil })
	require.NoError(t, err)
	d.Sched = s
	s.SetLeader(true)
	require.NoError(t, s.SyncFromDB())
	t.Cleanup(func() { _ = s.Shutdown() })
	// routes already registered by NewRouter; handlers read d.Sched at runtime.

	w := doJSON(r, http.MethodGet, "/api/scheduler/jobs", admin, nil)
	var list map[string]any
	require.NoError(t, unmarshalBody(w, &list))
	jobID := firstTaskJobID(t, list)

	w = doJSON(r, http.MethodPost, "/api/scheduler/jobs/"+jobID+"/run", admin, nil)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "task", out["kind"])
	require.NotNil(t, ranID, "exec ran through the service layer")

	// 404 unknown / 409 internal
	w = doJSON(r, http.MethodPost, "/api/scheduler/jobs/nope/run", admin, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
