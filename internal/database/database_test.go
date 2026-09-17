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

	// SystemSetting: raw column is setting_key (never the reserved `key`).
	setting := SystemSetting{Key: "site_title", Value: "Rclone Sync"}
	require.NoError(t, db.Create(&setting).Error)
	var gotSetting SystemSetting
	require.NoError(t, db.First(&gotSetting, "setting_key = ?", "site_title").Error)
	assert.Equal(t, "Rclone Sync", gotSetting.Value)
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

// Old installs carry system_settings_v2 with a `key` primary key (a MySQL
// reserved word). AutoMigrate must rename it in place — adding setting_key
// as a new PK column would fail on SQLite — and keep the data.
func TestAutoMigrateRenamesLegacySettingKeyColumn(t *testing.T) {
	db := openTestDB(t)

	require.NoError(t, db.Exec("CREATE TABLE system_settings_v2 (" +
		"`key` varchar(128) NOT NULL PRIMARY KEY, " +
		"`value` text NOT NULL, " +
		"updated_at datetime, updated_by_user_id integer)").Error)
	require.NoError(t, db.Exec("INSERT INTO system_settings_v2 " +
		"(`key`, `value`, updated_at) VALUES ('site_title', 'Legacy', '2026-01-02 03:04:05')").Error)

	require.NoError(t, AutoMigrate(db))

	mig := db.Migrator()
	assert.True(t, mig.HasColumn(&SystemSetting{}, "setting_key"))
	assert.False(t, mig.HasColumn(&SystemSetting{}, "key")) // raw name → old column gone

	var n int64
	require.NoError(t, db.Model(&SystemSetting{}).Where("setting_key = ?", "site_title").Count(&n).Error)
	assert.Equal(t, int64(1), n) // legacy row survived the rename

	// Re-running stays green (rename guard is a no-op once renamed).
	require.NoError(t, AutoMigrate(db))
}

// TestAutoMigrateAddsCreatorUserIDOnExistingTables reproduces the dev-DB
// upgrade path: a sync_tasks_v2 / check_tasks_v2 row already exists when
// the new build (which adds creator_user_id NOT NULL) is started. SQLite
// rejects ADD NOT NULL without a default; the field has default:0 so the
// ALTER TABLE runs cleanly and the legacy row keeps working.
func TestAutoMigrateAddsCreatorUserIDOnExistingTables(t *testing.T) {
	db := openTestDB(t)

	// Hand-craft the previous schema (no creator_user_id) + one legacy row.
	require.NoError(t, db.Exec(`
		CREATE TABLE sync_tasks_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			src_data_source_id INTEGER NOT NULL,
			src_path TEXT NOT NULL,
			dst_data_source_id INTEGER NOT NULL,
			dst_path TEXT NOT NULL,
			mode TEXT NOT NULL,
			cron TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			rclone_options TEXT NOT NULL,
			pre_check_task_id INTEGER,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE check_tasks_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			src_data_source_id INTEGER NOT NULL,
			src_path TEXT NOT NULL,
			dst_data_source_id INTEGER NOT NULL,
			dst_path TEXT NOT NULL,
			cron TEXT,
			enabled INTEGER NOT NULL,
			check_options TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO sync_tasks_v2
		(name, src_data_source_id, src_path, dst_data_source_id, dst_path, mode, cron, enabled, rclone_options, created_at, updated_at)
		VALUES ('legacy', 1, '/a', 2, '/b', 'sync', '0 3 * * *', 1, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO check_tasks_v2
		(name, src_data_source_id, src_path, dst_data_source_id, dst_path, cron, enabled, check_options, created_at, updated_at)
		VALUES ('legacy', 1, '/a', 2, '/b', NULL, 1, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error)

	// Upgrading a DB that already has rows must not fail with
	// "Cannot add a NOT NULL column with default value NULL".
	require.NoError(t, AutoMigrate(db))

	mig := db.Migrator()
	require.True(t, mig.HasColumn(&SyncTask{}, "creator_user_id"))
	require.True(t, mig.HasColumn(&CheckTask{}, "creator_user_id"))
	// Legacy row survives with creator_user_id = 0.
	var got SyncTask
	require.NoError(t, db.First(&got).Error)
	assert.EqualValues(t, 0, got.CreatorUserID)
	var gotCheck CheckTask
	require.NoError(t, db.First(&gotCheck).Error)
	assert.EqualValues(t, 0, gotCheck.CreatorUserID)
	// Re-running stays green.
	require.NoError(t, AutoMigrate(db))
}
