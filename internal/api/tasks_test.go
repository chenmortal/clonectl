package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"clonectl/internal/database"
	"clonectl/internal/rclone"
	"clonectl/internal/rclone/rctest"
	"clonectl/internal/services"
)

// tasksEnv: admin+edit users, one storage source + two data sources, fake rcd.
func tasksEnv(t *testing.T) (*Deps, *gin.Engine, string, string, *database.StorageSource, *rctest.Server) {
	t.Helper()
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	mkUser(t, d, "ed", "edit")
	admin := login(t, r, "root", "pass1234")
	edit := login(t, r, "ed", "pass1234")

	src := database.StorageSource{Name: "s3", Type: "s3", Extra: database.JSONObject{}}
	require.NoError(t, d.DB.Create(&src).Error)
	ak, sk := "ak", "sk"
	dsA := database.DataSource{Name: "dsA", StorageSourceID: src.ID, Path: "/a",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: 1}
	dsB := database.DataSource{Name: "dsB", StorageSourceID: src.ID, Path: "/b",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: 1}
	require.NoError(t, d.DB.Create(&dsA).Error)
	require.NoError(t, d.DB.Create(&dsB).Error)

	srv := rctest.New()
	t.Cleanup(srv.Close)
	d.RC = rclone.NewClient(srv.URL(), "u", "p", 0)
	return d, r, admin, edit, &src, srv
}

func TestTaskCRUDAndRBAC(t *testing.T) {
	d, r, admin, edit, _, _ := tasksEnv(t)

	body := func(name string) map[string]any {
		return map[string]any{
			"name": name, "src_data_source_id": 1, "dst_data_source_id": 2,
			"src_path": "/x", "dst_path": "/y", "mode": "sync",
			"cron": "0 3 * * *", "enabled": true,
			"rclone_options": map[string]any{"transfers": 4},
		}
	}

	// view cannot create
	mkUser(t, d, "viewer", "view")
	w := doJSON(r, http.MethodPost, "/api/tasks", login(t, r, "viewer", "pass1234"), body("v"))
	assert.Equal(t, http.StatusForbidden, w.Code)

	// edit CAN create (admin/edit allowed)
	w = doJSON(r, http.MethodPost, "/api/tasks", edit, body("t1"))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created map[string]any
	require.NoError(t, unmarshalBody(w, &created))
	assert.Equal(t, "t1", created["name"])
	assert.EqualValues(t, 1, created["src_data_source_id"])
	assert.Nil(t, created["src_storage_id"], "legacy field always null in output")
	assert.EqualValues(t, 4, created["rclone_options"].(map[string]any)["transfers"])
	taskID := itoa(int(created["id"].(float64)))

	// 404
	w = doJSON(r, http.MethodGet, "/api/tasks/999", admin, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "task not found")

	// List any role
	w = doJSON(r, http.MethodGet, "/api/tasks", login(t, r, "viewer", "pass1234"), nil)
	assert.Equal(t, http.StatusOK, w.Code)

	// Partial update keeps other fields + re-validates merged refs
	w = doJSON(r, http.MethodPut, "/api/tasks/"+taskID, admin, map[string]any{"cron": "0 4 * * 1"})
	require.Equal(t, http.StatusOK, w.Code)
	var upd map[string]any
	require.NoError(t, unmarshalBody(w, &upd))
	assert.Equal(t, "0 4 * * 1", upd["cron"])
	assert.Equal(t, "sync", upd["mode"], "unset field untouched")

	// Bad cron → 422
	w = doJSON(r, http.MethodPut, "/api/tasks/"+taskID, admin, map[string]any{"cron": "nope"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// Invalid mode → 422
	w = doJSON(r, http.MethodPut, "/api/tasks/"+taskID, admin, map[string]any{"mode": "rsync"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// Ref invariant: one-sided storage ref → 422
	w = doJSON(r, http.MethodPut, "/api/tasks/"+taskID, admin,
		map[string]any{"src_storage_id": 5})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "src: set either storage_id or data_source_id, not both")

	// Missing data source → 400
	w = doJSON(r, http.MethodPut, "/api/tasks/"+taskID, admin,
		map[string]any{"src_data_source_id": 999})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "data source 999 not found")

	// Delete → 204 (cascades runs)
	require.NoError(t, d.DB.Create(&database.SyncRun{TaskID: int64(created["id"].(float64)),
		Status: database.RunSuccess, Trigger: database.TriggerManual}).Error)
	w = doJSON(r, http.MethodDelete, "/api/tasks/"+taskID, admin, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = doJSON(r, http.MethodGet, "/api/tasks/"+taskID, admin, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTaskTrigger202AndPreCheck(t *testing.T) {
	d, r, admin, _, _, srv := tasksEnv(t)

	task := database.SyncTask{Name: "tt", SrcDataSourceID: 1, DstDataSourceID: 2,
		SrcPath: "/x", DstPath: "/y", Mode: database.ModeSync, Cron: "0 3 * * *",
		Enabled: true, RcloneOptions: database.JSONObject{}}
	require.NoError(t, d.DB.Create(&task).Error)

	// 202 + RunOut, synchronous
	w := doJSON(r, http.MethodPost, "/api/tasks/"+itoa(int(task.ID))+"/trigger", admin, nil)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var run map[string]any
	require.NoError(t, unmarshalBody(w, &run))
	assert.Equal(t, "running", run["status"])
	assert.Equal(t, "manual", run["trigger"])
	assert.NotNil(t, run["job_id"])

	// 404 on missing task
	w = doJSON(r, http.MethodPost, "/api/tasks/999/trigger", admin, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Runs listing + detail with live stitching
	srv.SetJobStats(int64(run["job_id"].(float64)), map[string]any{"bytes": float64(5)})
	w = doJSON(r, http.MethodGet, "/api/runs?task_id="+itoa(int(task.ID)), admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var page struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, unmarshalBody(w, &page))
	require.Len(t, page.Items, 1)

	w = doJSON(r, http.MethodGet, "/api/runs/"+itoa(int(run["id"].(float64))), admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var detail map[string]any
	require.NoError(t, unmarshalBody(w, &detail))
	live := detail["live"].(map[string]any)
	require.NotNil(t, live["status"], "running run carries live.status")
	require.NotNil(t, live["stats"], "running run carries live.stats")
	// totalBytes stitched from job/status when core/stats lacks it
	_ = srv
}

func TestRunsLiveTotalBytesStitch(t *testing.T) {
	d, r, admin, _, _, srv := tasksEnv(t)
	task := database.SyncTask{Name: "tt", SrcDataSourceID: 1, DstDataSourceID: 2,
		SrcPath: "/x", DstPath: "/y", Mode: database.ModeSync, Cron: "0 3 * * *",
		Enabled: true, RcloneOptions: database.JSONObject{}}
	require.NoError(t, d.DB.Create(&task).Error)

	w := doJSON(r, http.MethodPost, "/api/tasks/"+itoa(int(task.ID))+"/trigger", admin, nil)
	require.Equal(t, http.StatusAccepted, w.Code)
	var run map[string]any
	require.NoError(t, unmarshalBody(w, &run))
	runID := itoa(int(run["id"].(float64)))
	jobID := int64(run["job_id"].(float64))

	// While running: core/stats lacks totalBytes, job/status.stats carries it
	// — the stitch into live.stats is load-bearing for the UI progress bar.
	srv.SetJobStats(int64(run["job_id"].(float64)), map[string]any{"bytes": float64(1), "speed": float64(2)})
	srv.SetDefaultJobStatus(map[string]any{
		"finished": false, "success": true,
		"stats": map[string]any{"totalBytes": float64(100)},
	})
	w = doJSON(r, http.MethodGet, "/api/runs/"+runID, admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var detail map[string]any
	require.NoError(t, unmarshalBody(w, &detail))
	live := detail["live"].(map[string]any)
	stats := live["stats"].(map[string]any)
	assert.EqualValues(t, 1, stats["bytes"])
	assert.EqualValues(t, 100, stats["totalBytes"], "totalBytes stitched from job/status")

	// Finished → no live payload at all.
	srv.SetDefaultJobStatus(nil)
	srv.QueueJobStatus(jobID, rctest.FinishedSuccess(jobID))
	// advance the run to success via the poller
	_, err := services.PollRun(d.DB, d.RC, int64(run["id"].(float64)))
	require.NoError(t, err)
	w = doJSON(r, http.MethodGet, "/api/runs/"+runID, admin, nil)
	require.NoError(t, unmarshalBody(w, &detail))
	assert.Nil(t, detail["live"], "finished run has no live payload")
	assert.Equal(t, "success", detail["status"])
}

func TestCheckTaskCRUDAndTrigger(t *testing.T) {
	d, r, admin, _, _, _ := tasksEnv(t)

	// Create with nullable cron (manual-only)
	w := doJSON(r, http.MethodPost, "/api/check-tasks", admin, map[string]any{
		"name": "chk", "src_data_source_id": 1, "dst_data_source_id": 2,
		"src_path": "/x", "dst_path": "/y", "cron": nil,
		"check_options": map[string]any{"oneWay": true},
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created map[string]any
	require.NoError(t, unmarshalBody(w, &created))
	assert.Nil(t, created["cron"])
	assert.EqualValues(t, true, created["check_options"].(map[string]any)["oneWay"])
	checkID := itoa(int(created["id"].(float64)))

	// Delete protection: referenced as pre-check → 409
	task := database.SyncTask{Name: "with-pre", SrcDataSourceID: 1, DstDataSourceID: 2,
		SrcPath: "/x", DstPath: "/y", Mode: database.ModeSync, Cron: "0 3 * * *", Enabled: true,
		RcloneOptions: database.JSONObject{}}
	preID := int64(created["id"].(float64))
	task.PreCheckTaskID = &preID
	require.NoError(t, d.DB.Create(&task).Error)
	w = doJSON(r, http.MethodDelete, "/api/check-tasks/"+checkID, admin, nil)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "used as pre-check")

	// Trigger → 202 CheckOut
	w = doJSON(r, http.MethodPost, "/api/check-tasks/"+checkID+"/trigger", admin, nil)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var check map[string]any
	require.NoError(t, unmarshalBody(w, &check))
	assert.Equal(t, "running", check["status"])

	// Checks listing
	w = doJSON(r, http.MethodGet, "/api/checks", admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var page struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, unmarshalBody(w, &page))
	assert.Len(t, page.Items, 1)

	// Check detail with live
	w = doJSON(r, http.MethodGet, "/api/checks/"+itoa(int(check["id"].(float64))), admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var detail map[string]any
	require.NoError(t, unmarshalBody(w, &detail))
	require.NotNil(t, detail["live"].(map[string]any)["status"], "check live has status only")
	_, hasStats := detail["live"].(map[string]any)["stats"]
	assert.False(t, hasStats, "check live never carries stats")
}
