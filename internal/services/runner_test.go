package services

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"clonectl/internal/database"
	"clonectl/internal/rclone"
	"clonectl/internal/rclone/rctest"
)

// runnerEnv: DB + fake rcd + one s3 source + two data sources + one local
// source with an FS prefix + its data source + one task.
func runnerEnv(t *testing.T) (*gorm.DB, *rclone.Client, *rctest.Server, *database.SyncTask) {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })

	admin := database.User{Username: "root", PasswordHash: "x", Role: database.RoleAdmin}
	require.NoError(t, db.Create(&admin).Error)
	src := database.StorageSource{Name: "s3", Type: "s3", Extra: database.JSONObject{"provider": "Minio"}}
	require.NoError(t, db.Create(&src).Error)
	root := "/srv/roots"
	fsSrc := database.StorageSource{Name: "fs", Type: "local", Path: &root,
		Extra: database.JSONObject{}}
	require.NoError(t, db.Create(&fsSrc).Error)
	ak, sk := "ak", "sk"
	dsA := database.DataSource{Name: "a", StorageSourceID: src.ID, Path: "/data",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: admin.ID}
	dsB := database.DataSource{Name: "b", StorageSourceID: src.ID, Path: "/backup/",
		AccessKeyID: &ak, SecretAccessKey: &sk, OwnerUserID: admin.ID}
	dsL := database.DataSource{Name: "l", StorageSourceID: fsSrc.ID, Path: "/data",
		OwnerUserID: admin.ID}
	require.NoError(t, db.Create(&dsA).Error)
	require.NoError(t, db.Create(&dsB).Error)
	require.NoError(t, db.Create(&dsL).Error)

	task := database.SyncTask{Name: "nightly", SrcDataSourceID: dsA.ID, DstDataSourceID: dsB.ID,
		SrcPath: "sub", DstPath: "sub", Mode: database.ModeSync, Cron: "0 3 * * *", Enabled: true,
		RcloneOptions: database.JSONObject{"transfers": float64(4)}}
	require.NoError(t, db.Create(&task).Error)

	srv := rctest.New()
	t.Cleanup(srv.Close)
	client := rclone.NewClient(srv.URL(), "u", "p", 0)
	return db, client, srv, &task
}

func TestRunTaskSuccess(t *testing.T) {
	db, client, srv, task := runnerEnv(t)

	run, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	require.Equal(t, database.RunRunning, run.Status)
	require.NotNil(t, run.JobID)
	assert.NotNil(t, run.StartedAt)

	// Remote pushed for both sides, sync submitted with joined fs specs.
	rec := srv.Records()
	require.Len(t, rec, 3) // 2× config/create + sync
	assert.Equal(t, "/sync/sync", rec[2].Path)
	assert.Equal(t, "ds-1:/data/sub", rec[2].Body["srcFs"])
	assert.Equal(t, "ds-2:/backup/sub", rec[2].Body["dstFs"], "trailing slash on ds.path stripped")
	cfg := rec[2].Body["_config"].(map[string]any)
	// 规范名 transfers 被翻译成 rc 线上字段名 Transfers(见 rclone client)。
	assert.EqualValues(t, 4, cfg["Transfers"])

	// Poll a finished job → success with the 7 stat keys only.
	srv.SetJobStats(*run.JobID, map[string]any{
		"bytes": float64(10), "checks": float64(1), "transfers": float64(2),
		"errors": float64(0), "elapsedTime": float64(1.5), "totalBytes": float64(100),
		"totalTransfers": float64(3), "speed": float64(99),
	})
	polled, err := PollRun(db, client, run.ID)
	require.NoError(t, err)
	assert.Equal(t, database.RunSuccess, polled.Status)
	require.NotNil(t, polled.Stats)
	assert.EqualValues(t, 10, (*polled.Stats)["bytes"])
	_, hasSpeed := (*polled.Stats)["speed"]
	assert.False(t, hasSpeed, "only the 7 known stat keys are kept")
	_, hasJobStatus := (*polled.Stats)["jobStatus"]
	assert.False(t, hasJobStatus, "success embeds nothing extra")
	assert.NotNil(t, polled.FinishedAt)
}

func TestRunTaskConcurrencyGuardSkips(t *testing.T) {
	db, client, _, task := runnerEnv(t)

	first, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	require.Equal(t, database.RunRunning, first.Status)

	second, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunSkipped, second.Status)
	assert.Equal(t, "previous run still running", *second.Error)
	assert.Equal(t, second.StartedAt, second.FinishedAt)
}

func TestRunTaskStartFailure(t *testing.T) {
	db, client, srv, task := runnerEnv(t)
	srv.FailNext("/sync/sync", 500, map[string]any{"error": "bucket missing"})

	run, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	assert.Equal(t, database.RunFailed, run.Status)
	require.NotNil(t, run.Error)
	assert.Contains(t, *run.Error, "returned 500")
	assert.Contains(t, *run.Error, "rclone: bucket missing")
	assert.NotNil(t, run.StartedAt)
	assert.NotNil(t, run.FinishedAt)
}

func TestPollRunFailureEmbedsJobStatus(t *testing.T) {
	db, client, srv, task := runnerEnv(t)
	run, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)

	srv.QueueJobStatus(*run.JobID, rctest.FinishedError(*run.JobID, "sync failed: NoSuchBucket"))
	polled, err := PollRun(db, client, run.ID)
	require.NoError(t, err)
	assert.Equal(t, database.RunFailed, polled.Status)
	assert.Equal(t, "sync failed: NoSuchBucket", *polled.Error)
	require.NotNil(t, polled.Stats)
	assert.Contains(t, (*polled.Stats)["jobStatus"], "error", "jobStatus embedded on failure")
}

func TestPollRunningRunsAdvancesAll(t *testing.T) {
	db, client, srv, task := runnerEnv(t)
	run, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	srv.QueueJobStatus(*run.JobID, rctest.Running(*run.JobID), rctest.FinishedSuccess(*run.JobID))

	PollRunningRuns(db, client)
	var after database.SyncRun
	require.NoError(t, db.First(&after, run.ID).Error)
	assert.Equal(t, database.RunRunning, after.Status, "first poll: still running")

	PollRunningRuns(db, client)
	require.NoError(t, db.First(&after, run.ID).Error)
	assert.Equal(t, database.RunSuccess, after.Status, "second poll: finished")
}

func TestJoinDSPath(t *testing.T) {
	cases := []struct{ ds, sub, want string }{
		{"", "", ""},
		{"", "sub", "sub"},
		{"/", "sub", "sub"},
		{"/data", "sub", "/data/sub"},
		{"/data/", "sub", "/data/sub"},
		{"/data", "/sub", "/data/sub"},
		{"/data/", "/sub", "/data/sub"},
		{"/data", "", "/data"},
		{"data/", "deep/sub", "data/deep/sub"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, JoinDSPath(tc.ds, tc.sub), "JoinDSPath(%q,%q)", tc.ds, tc.sub)
	}
}

func TestSidePath(t *testing.T) {
	root := "/srv/roots"
	slashRoot := "/srv/roots/"
	cases := []struct {
		name   string
		src    database.StorageSource
		dsPath string
		want   string
	}{
		{"local bakes path column prefix", database.StorageSource{Type: "local", Path: &root}, "/bucket/data", "/srv/roots/bucket/data"},
		{"local prefix trailing slash stripped", database.StorageSource{Type: "local", Path: &slashRoot}, "/data", "/srv/roots/data"},
		{"local extra.root fallback", database.StorageSource{Type: "local", Extra: database.JSONObject{"root": "/alt"}}, "/data", "/alt/data"},
		{"local no prefix degenerates to slash root", database.StorageSource{Type: "local"}, "/data", "data"},
		{"s3 keeps bucket path as-is", database.StorageSource{Type: "s3", Extra: database.JSONObject{"provider": "Minio"}}, "/bucket/data", "/bucket/data"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, SidePath(&tc.src, tc.dsPath), tc.name)
	}
}

func TestRunTaskLocalPrefixBakesIntoFsSpec(t *testing.T) {
	db, client, srv, task := runnerEnv(t)
	var dsL database.DataSource
	require.NoError(t, db.Where("name = ?", "l").First(&dsL).Error)
	require.NoError(t, db.Model(&task).Updates(map[string]any{
		"src_data_source_id": dsL.ID, "dst_data_source_id": dsL.ID,
	}).Error)

	run, err := RunTask(db, client, 3600, task.ID, database.TriggerManual)
	require.NoError(t, err)
	require.Equal(t, database.RunRunning, run.Status)

	rec := srv.Records()
	require.Len(t, rec, 3) // config/create (ds-3) ×2 + sync
	spec := fmt.Sprintf("ds-%d:/srv/roots/data/sub", dsL.ID)
	assert.Equal(t, spec, rec[2].Body["srcFs"], "local FS prefix baked into the path")
	assert.Equal(t, spec, rec[2].Body["dstFs"])
	params := rec[0].Body["parameters"].(map[string]any)
	assert.Equal(t, "local", rec[0].Body["type"])
	_, hasRoot := params["root"]
	assert.False(t, hasRoot, "local backend has no root option; never send one")
	_, hasAK := params["access_key_id"]
	assert.False(t, hasAK, "local remote carries no credentials")
}
