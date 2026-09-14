package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
	"rclone_sync/internal/rclone/rctest"
)

// checkEnv: DB + fake rcd + one task (shares the shape of runnerEnv).
func checkEnv(t *testing.T) (*gorm.DB, *rclone.Client, *rctest.Server, *database.CheckTask) {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })

	admin := database.User{Username: "root", PasswordHash: "x", Role: database.RoleAdmin}
	require.NoError(t, db.Create(&admin).Error)
	src := database.StorageSource{Name: "s3", Type: "s3", Extra: database.JSONObject{}}
	require.NoError(t, db.Create(&src).Error)
	ak, sk := "ak", "sk"
	dsA := database.DataSource{Name: "a", StorageSourceID: src.ID, Path: "/data",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: admin.ID}
	dsB := database.DataSource{Name: "b", StorageSourceID: src.ID, Path: "/backup",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: admin.ID}
	require.NoError(t, db.Create(&dsA).Error)
	require.NoError(t, db.Create(&dsB).Error)

	oneWay := true
	task := database.CheckTask{Name: "check-it", SrcDataSourceID: dsA.ID, DstDataSourceID: dsB.ID,
		SrcPath: "sub", DstPath: "sub", Enabled: true,
		CheckOptions: database.JSONObject{"oneWay": oneWay}}
	require.NoError(t, db.Create(&task).Error)

	srv := rctest.New()
	t.Cleanup(srv.Close)
	client := rclone.NewClient(srv.URL(), "u", "p", 0)
	return db, client, srv, &task
}

func checkOutput(success bool, differ, missingSrc, missingDst, errs int) map[string]any {
	list := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{}
		}
		return out
	}
	return map[string]any{
		"success":      success,
		"differ":       list(differ),
		"missingOnSrc": list(missingSrc),
		"missingOnDst": list(missingDst),
		"error":        list(errs),
	}
}

func TestRunCheckSuccess(t *testing.T) {
	db, client, srv, task := checkEnv(t)

	check, err := RunCheck(db, client, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunRunning, check.Status)
	require.NotNil(t, check.JobID)

	// Options merged at payload top level.
	rec := srv.Records()
	require.Len(t, rec, 3)
	assert.Equal(t, "/operations/check", rec[2].Path)
	assert.Equal(t, true, rec[2].Body["oneWay"])
	assert.Equal(t, "ds-1:/data/sub", rec[2].Body["srcFs"])

	// Finish successful & consistent → success with picked result keys.
	status := rctest.FinishedSuccess(*check.JobID)
	status["output"] = checkOutput(true, 0, 0, 0, 0)
	srv.QueueJobStatus(*check.JobID, status)
	polled, err := PollCheck(db, client, check.ID)
	require.NoError(t, err)
	assert.Equal(t, database.RunSuccess, polled.Status)
	require.NotNil(t, polled.Result)
	assert.Equal(t, true, (*polled.Result)["success"])
	assert.Contains(t, (*polled.Result), "differ", "result keys picked from output")
	_, hasRaw := (*polled.Result)["nonexistent"]
	assert.False(t, hasRaw)
}

func TestRunCheckDifferencesFail(t *testing.T) {
	db, client, srv, task := checkEnv(t)
	check, err := RunCheck(db, client, task.ID, database.TriggerManual)
	require.NoError(t, err)

	status := rctest.FinishedSuccess(*check.JobID)
	status["output"] = checkOutput(false, 2, 1, 3, 4)
	srv.QueueJobStatus(*check.JobID, status)
	polled, err := PollCheck(db, client, check.ID)
	require.NoError(t, err)
	assert.Equal(t, database.RunFailed, polled.Status)
	require.NotNil(t, polled.Error)
	assert.Contains(t, *polled.Error, "一致性检查发现差异：2 个文件不一致")
	assert.Contains(t, *polled.Error, "源端缺失 1，目标端缺失 3，错误 4")
}

func TestRunCheckJobFailure(t *testing.T) {
	db, client, srv, task := checkEnv(t)
	check, err := RunCheck(db, client, task.ID, database.TriggerManual)
	require.NoError(t, err)

	srv.QueueJobStatus(*check.JobID, rctest.FinishedError(*check.JobID, "no such bucket"))
	polled, err := PollCheck(db, client, check.ID)
	require.NoError(t, err)
	assert.Equal(t, database.RunFailed, polled.Status)
	assert.Equal(t, "no such bucket", *polled.Error)
}

func TestRunCheckStartFailure(t *testing.T) {
	db, client, srv, task := checkEnv(t)
	srv.FailNext("/operations/check", 500, map[string]any{"error": "denied"})

	check, err := RunCheck(db, client, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunFailed, check.Status)
	assert.Contains(t, *check.Error, "denied")
}

func TestWaitForCheckTimeout(t *testing.T) {
	db, client, _, task := checkEnv(t)
	check, err := RunCheck(db, client, task.ID, database.TriggerManual)
	require.NoError(t, err)

	// Timeout of 0s fires immediately; job still running → marked failed.
	done, err := WaitForCheck(db, client, check.ID, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, database.RunFailed, done.Status)
	assert.Contains(t, *done.Error, "check timed out after 0s")
}

func TestPreCheckFlowSkipProceedBlock(t *testing.T) {
	// The pre-check inside RunTask creates its own check row + jobid, so the
	// fake server's DEFAULT job/status response drives all three outcomes.
	db, client, srv, task := runnerEnv(t)
	ckSrc := database.StorageSource{Name: "cs3", Type: "s3", Extra: database.JSONObject{}}
	require.NoError(t, db.Create(&ckSrc).Error)
	ak, sk := "ak", "sk"
	dsA := database.DataSource{Name: "ca", StorageSourceID: ckSrc.ID, Path: "/d",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: 1}
	dsB := database.DataSource{Name: "cb", StorageSourceID: ckSrc.ID, Path: "/d",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: 1}
	require.NoError(t, db.Create(&dsA).Error)
	require.NoError(t, db.Create(&dsB).Error)
	preTask := database.CheckTask{Name: "pre", SrcDataSourceID: dsA.ID, DstDataSourceID: dsB.ID,
		SrcPath: "", DstPath: "", Enabled: true, CheckOptions: database.JSONObject{}}
	require.NoError(t, db.Create(&preTask).Error)
	task.PreCheckTaskID = &preTask.ID
	require.NoError(t, db.Save(task).Error)

	// Case 1: consistent pre-check → sync skipped with the pass message.
	okStatus := rctest.FinishedSuccess(0)
	okStatus["output"] = checkOutput(true, 0, 0, 0, 0)
	srv.SetDefaultJobStatus(okStatus)
	run, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunSkipped, run.Status)
	assert.Contains(t, *run.Error, "同步前一致性检查通过")
	assert.Contains(t, *run.Error, "两端一致，跳过同步")

	// Case 2: differences → sync proceeds to running.
	diffStatus := rctest.FinishedSuccess(0)
	diffStatus["output"] = checkOutput(false, 5, 0, 0, 0)
	srv.SetDefaultJobStatus(diffStatus)
	run2, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunRunning, run2.Status, "differences → proceed")

	// Finish run2's sync job so case 3 passes the concurrency guard.
	srv.QueueJobStatus(*run2.JobID, rctest.FinishedSuccess(*run2.JobID))
	_, err = PollRun(db, client, run2.ID)
	require.NoError(t, err)

	// Case 3: check error → blocked.
	srv.SetDefaultJobStatus(rctest.FinishedError(0, "boom"))
	run3, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunSkipped, run3.Status)
	assert.Contains(t, *run3.Error, "同步前一致性检查出错")
	assert.Contains(t, *run3.Error, "已阻止同步")
}
