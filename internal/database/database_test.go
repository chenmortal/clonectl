package database

import (
	"testing"

	"gorm.io/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestDB returns an isolated in-memory SQLite DB (shared cache so GORM's
// single pooled connection sees one database).
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		_ = sqlDB.Close()
	})
	return gdb
}

func TestAutoMigrateAndCRUD(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, AutoMigrate(db))

	u := User{Username: "alice", PasswordHash: "h", Role: RoleAdmin}
	require.NoError(t, db.Create(&u).Error)
	require.NotZero(t, u.ID)

	got := User{}
	require.NoError(t, db.First(&got, u.ID).Error)
	assert.Equal(t, "alice", got.Username)
	assert.False(t, got.CreatedAt.IsZero())

	// JSONObject round-trip
	ss := StorageSource{Name: "s3-main", Type: "s3", Extra: JSONObject{"provider": "Minio", "retries": float64(3)}}
	require.NoError(t, db.Create(&ss).Error)
	var gotSS StorageSource
	require.NoError(t, db.First(&gotSS, ss.ID).Error)
	assert.Equal(t, "Minio", gotSS.Extra["provider"])

	// Unique constraint on (owner, name)
	ds := DataSource{Name: "d1", StorageSourceID: ss.ID, Path: "/data", OwnerUserID: u.ID}
	require.NoError(t, db.Create(&ds).Error)
	dup := DataSource{Name: "d1", StorageSourceID: ss.ID, Path: "/data", OwnerUserID: u.ID}
	require.Error(t, db.Create(&dup).Error)

	// Task/run persistence
	task := SyncTask{Name: "t", SrcDataSourceID: ds.ID, DstDataSourceID: ds.ID,
		SrcPath: "/a", DstPath: "/b", Mode: ModeSync, Cron: "0 3 * * *", Enabled: true,
		RcloneOptions: JSONObject{}}
	require.NoError(t, db.Create(&task).Error)
	run := SyncRun{TaskID: task.ID, Status: RunPending, Trigger: TriggerManual}
	require.NoError(t, db.Create(&run).Error)
	var gotRun SyncRun
	require.NoError(t, db.First(&gotRun, run.ID).Error)
	assert.Equal(t, RunPending, gotRun.Status)
	assert.Nil(t, gotRun.Stats)
}

func TestJSONObjectScanNilAndRaw(t *testing.T) {
	var j JSONObject
	require.NoError(t, j.Scan(nil))
	assert.Nil(t, j)

	require.NoError(t, j.Scan([]byte(`{"a":1}`)))
	assert.Equal(t, float64(1), j["a"])

	// Malformed JSON is preserved under "raw" instead of erroring.
	require.NoError(t, j.Scan([]byte(`not-json`)))
	assert.Equal(t, "not-json", j["raw"])
}
