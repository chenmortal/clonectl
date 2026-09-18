package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"clonectl/internal/database"
)

// legacySchema creates Python-shaped legacy tables in SQLite.
func legacySchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL, role TEXT NOT NULL, created_at DATETIME,
			last_login_at DATETIME, disabled_at DATETIME)`,
		// storage_configs already exists from AutoMigrate (legacy-compat shape).
		`CREATE TABLE storage_sources (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL,
			type TEXT NOT NULL, endpoint TEXT, region TEXT, extra TEXT NOT NULL,
			created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE data_sources (id INTEGER PRIMARY KEY, name TEXT NOT NULL,
			storage_source_id INTEGER NOT NULL, path TEXT NOT NULL, access_key_id TEXT,
			secret_access_key TEXT, description TEXT, owner_user_id INTEGER NOT NULL,
			last_verified_at DATETIME, last_verified_ok BOOLEAN,
			created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE data_source_bindings (id INTEGER PRIMARY KEY, data_source_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL, permission TEXT NOT NULL, created_by_user_id INTEGER,
			created_at DATETIME)`,
		`CREATE TABLE system_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
			updated_at DATETIME, updated_by_user_id INTEGER)`,
		`CREATE TABLE sync_tasks (id INTEGER PRIMARY KEY, name TEXT NOT NULL,
			src_storage_id INTEGER, src_path TEXT NOT NULL, dst_storage_id INTEGER,
			dst_path TEXT NOT NULL, src_data_source_id INTEGER, dst_data_source_id INTEGER,
			mode TEXT NOT NULL, cron TEXT NOT NULL, enabled BOOLEAN NOT NULL,
			rclone_options TEXT NOT NULL, pre_check_task_id INTEGER,
			created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE check_tasks (id INTEGER PRIMARY KEY, name TEXT NOT NULL,
			src_storage_id INTEGER, src_path TEXT NOT NULL, dst_storage_id INTEGER,
			dst_path TEXT NOT NULL, src_data_source_id INTEGER, dst_data_source_id INTEGER,
			cron TEXT, enabled BOOLEAN NOT NULL, check_options TEXT NOT NULL,
			created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE sync_runs (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL,
			job_id INTEGER, status TEXT NOT NULL, trigger TEXT NOT NULL,
			started_at DATETIME, finished_at DATETIME, error TEXT, stats TEXT, notified_at DATETIME)`,
		`CREATE TABLE check_runs (id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL,
			job_id INTEGER, status TEXT NOT NULL, trigger TEXT NOT NULL,
			started_at DATETIME, finished_at DATETIME, error TEXT, result TEXT, notified_at DATETIME)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error)
	}
}

func migrateDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	legacySchema(t, db)
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}

func TestMigrateCopiesLegacyData(t *testing.T) {
	db := migrateDB(t)

	// Legacy rows: one user, one legacy storage config (to fan out), one
	// v2-era data source + task referencing it, one legacy task referencing
	// the storage id (needs remap), and one run.
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at)
		VALUES (1, 'admin', '$2a$12$hash', 'admin', '2026-01-01 00:00:00')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO storage_configs (id, name, type, parameters, created_at, updated_at)
		VALUES (9, 'minio-old', 's3', '{"endpoint":"http://s:9000","access_key_id":"ak","secret_access_key":"sk","provider":"Minio"}',
		'2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error)
	// v2-era storage source + data source (ds id 5) + bindings + settings.
	require.NoError(t, db.Exec(`INSERT INTO storage_sources (id, name, type, extra, created_at, updated_at)
		VALUES (2, 's3new', 's3', '{}', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO data_sources (id, name, storage_source_id, path, owner_user_id, created_at, updated_at)
		VALUES (5, 'ds5', 2, '/data', 1, '2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO data_source_bindings (id, data_source_id, user_id, permission, created_at)
		VALUES (7, 5, 1, 'read', '2026-01-01 00:00:00')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO system_settings (key, value, updated_at)
		VALUES ('alertmanager_url', 'http://am:9093', '2026-01-01 00:00:00')`).Error)
	// Task with ds refs (5↔5) + pre-check-free.
	require.NoError(t, db.Exec(`INSERT INTO sync_tasks (id, name, src_data_source_id, dst_data_source_id,
		src_path, dst_path, mode, cron, enabled, rclone_options, created_at, updated_at)
		VALUES (11, 't-ds', 5, 5, '/x', '/y', 'sync', '0 3 * * *', 1, '{}',
		'2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error)
	// Task with legacy storage refs (9→9): needs migration_log remap.
	require.NoError(t, db.Exec(`INSERT INTO sync_tasks (id, name, src_storage_id, dst_storage_id,
		src_path, dst_path, mode, cron, enabled, rclone_options, created_at, updated_at)
		VALUES (12, 't-legacy', 9, 9, '/x', '/y', 'copy', '0 4 * * *', 1, '{}',
		'2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO sync_runs (id, task_id, job_id, status, trigger)
		VALUES (21, 11, 77, 'success', 'manual')`).Error)

	counts, err := Run(db)
	require.NoError(t, err)
	assert.Equal(t, 1, counts.Users)
	assert.Equal(t, 0, counts.SyncTasksSkipped, "task 12 has a migration_log mapping")

	// IDs preserved → old tokens/FKs work.
	var u database.User
	require.NoError(t, db.First(&u, 1).Error)
	assert.Equal(t, "$2a$12$hash", u.PasswordHash, "hash verbatim")

	// Fan-out created the source + default ds and remapped task 12.
	var t12 database.SyncTask
	require.NoError(t, db.First(&t12, 12).Error)
	require.NotZero(t, t12.SrcDataSourceID, "legacy refs remapped")
	var log database.MigrationLog
	require.NoError(t, db.First(&log, "table_name = ? AND legacy_id = 9", "storage_config").Error)
	assert.Equal(t, log.NewID, t12.SrcDataSourceID)
	assert.Equal(t, log.NewID, t12.DstDataSourceID)

	// Task 11 refs untouched; run copied.
	var t11 database.SyncTask
	require.NoError(t, db.First(&t11, 11).Error)
	assert.EqualValues(t, 5, t11.SrcDataSourceID)
	var run database.SyncRun
	require.NoError(t, db.First(&run, 21).Error)
	assert.Equal(t, "success", run.Status)

	// Idempotent: rerun changes nothing.
	counts2, err := Run(db)
	require.NoError(t, err)
	assert.Equal(t, 0, counts2.Users)
	assert.Equal(t, 0, counts2.SyncTasks)
	var userCount, taskCount int64
	db.Model(&database.User{}).Count(&userCount)
	db.Model(&database.SyncTask{}).Count(&taskCount)
	assert.EqualValues(t, 1, userCount)
	assert.EqualValues(t, 2, taskCount)
}

func TestMigrateNoLegacyTables(t *testing.T) {
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })

	_, err = Run(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no legacy tables")
}
