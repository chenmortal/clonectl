// Package migrate copies legacy Python-schema tables into the redesigned
// *_v2 schema. IDs are preserved verbatim so existing JWTs (sub=user id),
// task foreign keys and migration_log rows stay valid. Per-row idempotency:
// rows whose id already exists in the target are skipped, so re-running is
// safe and reports what it skipped. Legacy rows are never modified.
package migrate

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"rclone_sync/internal/database"
	"rclone_sync/internal/services"
)

// Counts summarizes one Run.
type Counts struct {
	Users                  int
	StorageSources         int
	DataSources            int
	Bindings               int
	Settings               int
	Skipped                map[string]int
	StorageConfigsMigrated int
	SourcesCreated         int
	DatasourcesCreated     int
	CheckTasks             int
	CheckTasksSkipped      int
	SyncTasks              int
	SyncTasksSkipped       int
	SyncRuns               int
	CheckRuns              int
}

// HasLegacyTables reports whether the legacy user table exists (the marker
// for "this DB was ever used by the Python build").
func HasLegacyTables(db *gorm.DB) bool {
	return db.Migrator().HasTable("users")
}

// Run copies everything. Order is FK-safe: users → storage_configs fan-out
// (creates the migration_log the task remap needs) → sources → data sources
// → bindings → settings → check tasks (before sync: pre_check FK) → sync
// tasks → runs.
func Run(db *gorm.DB) (Counts, error) {
	c := Counts{Skipped: map[string]int{}}
	if !HasLegacyTables(db) {
		return c, fmt.Errorf("no legacy tables found (users missing); nothing to migrate")
	}

	// users (bcrypt hashes copied as-is → old tokens keep working).
	if err := forEach(db, "users", func(r legacyUser) error {
		return insertIfAbsent(db, &database.User{ID: r.ID}, &database.User{
			ID: r.ID, Username: r.Username, PasswordHash: r.PasswordHash,
			Role: r.Role, CreatedAt: r.CreatedAt,
			LastLoginAt: r.LastLoginAt, DisabledAt: r.DisabledAt,
		}, &c.Users, "users", c.Skipped)
	}); err != nil {
		return c, fmt.Errorf("users: %w", err)
	}

	// storage_configs fan-out → sources + default data sources + migration_log.
	if db.Migrator().HasTable("storage_configs") {
		counts, err := services.MigrateLegacyStorageConfigs(db)
		if err != nil {
			return c, fmt.Errorf("storage_configs fan-out: %w", err)
		}
		c.StorageConfigsMigrated = counts.SourcesCreated + counts.SkippedAlreadyMigrated
		c.SourcesCreated = counts.SourcesCreated
		c.DatasourcesCreated = counts.DatasourcesCreated
	}

	// storage_sources.
	if db.Migrator().HasTable("storage_sources") {
		if err := forEach(db, "storage_sources", func(r legacyStorageSource) error {
			return insertIfAbsent(db, &database.StorageSource{ID: r.ID}, &database.StorageSource{
				ID: r.ID, Name: r.Name, Type: r.Type,
				Endpoint: r.Endpoint, Region: r.Region, Extra: derefJSON(r.Extra),
				CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			}, &c.StorageSources, "storage_sources", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("storage_sources: %w", err)
		}
	}

	// data_sources (IDs preserved → task ds refs stay valid).
	if db.Migrator().HasTable("data_sources") {
		if err := forEach(db, "data_sources", func(r legacyDataSource) error {
			return insertIfAbsent(db, &database.DataSource{ID: r.ID}, &database.DataSource{
				ID: r.ID, Name: r.Name, StorageSourceID: r.StorageSourceID, Path: r.Path,
				AccessKeyID: r.AccessKeyID, SecretAccessKey: r.SecretAccessKey,
				Description: r.Description, OwnerUserID: r.OwnerUserID,
				LastVerifiedAt: r.LastVerifiedAt, LastVerifiedOK: r.LastVerifiedOK,
				CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			}, &c.DataSources, "data_sources", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("data_sources: %w", err)
		}
	}

	// bindings.
	if db.Migrator().HasTable("data_source_bindings") {
		if err := forEach(db, "data_source_bindings", func(r legacyBinding) error {
			return insertIfAbsent(db, &database.DataSourceBinding{ID: r.ID}, &database.DataSourceBinding{
				ID: r.ID, DataSourceID: r.DataSourceID, UserID: r.UserID,
				Permission: r.Permission, CreatedByUserID: r.CreatedByUserID,
				CreatedAt: r.CreatedAt,
			}, &c.Bindings, "data_source_bindings", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("data_source_bindings: %w", err)
		}
	}

	// system_settings.
	if db.Migrator().HasTable("system_settings") {
		if err := forEach(db, "system_settings", func(r legacySetting) error {
			return insertIfAbsent(db, &database.SystemSetting{Key: r.Key}, &database.SystemSetting{
				Key: r.Key, Value: r.Value, UpdatedAt: r.UpdatedAt, UpdatedByUserID: r.UpdatedByUserID,
			}, &c.Settings, "system_settings", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("system_settings: %w", err)
		}
	}

	// check_tasks (before sync_tasks: pre_check_task_id FK).
	if db.Migrator().HasTable("check_tasks") {
		if err := forEach(db, "check_tasks", func(r legacyCheckTask) error {
			src, ok1 := remapSide(db, r.SrcStorageID, r.SrcDataSourceID)
			dst, ok2 := remapSide(db, r.DstStorageID, r.DstDataSourceID)
			if !ok1 || !ok2 {
				c.CheckTasksSkipped++
				c.Skipped["check_tasks"]++
				slog.Warn("migrate: check task has unmapped legacy storage ref; skipped", "id", r.ID)
				return nil
			}
			return insertIfAbsent(db, &database.CheckTask{ID: r.ID}, &database.CheckTask{
				ID: r.ID, Name: r.Name,
				SrcDataSourceID: src, SrcPath: r.SrcPath,
				DstDataSourceID: dst, DstPath: r.DstPath,
				Cron: r.Cron, Enabled: r.Enabled, CheckOptions: derefJSON(r.CheckOptions),
				CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			}, &c.CheckTasks, "check_tasks", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("check_tasks: %w", err)
		}
	}

	// sync_tasks.
	if db.Migrator().HasTable("sync_tasks") {
		if err := forEach(db, "sync_tasks", func(r legacySyncTask) error {
			src, ok1 := remapSide(db, r.SrcStorageID, r.SrcDataSourceID)
			dst, ok2 := remapSide(db, r.DstStorageID, r.DstDataSourceID)
			if !ok1 || !ok2 {
				c.SyncTasksSkipped++
				c.Skipped["sync_tasks"]++
				slog.Warn("migrate: sync task has unmapped legacy storage ref; skipped", "id", r.ID)
				return nil
			}
			return insertIfAbsent(db, &database.SyncTask{ID: r.ID}, &database.SyncTask{
				ID: r.ID, Name: r.Name,
				SrcDataSourceID: src, SrcPath: r.SrcPath,
				DstDataSourceID: dst, DstPath: r.DstPath,
				Mode: r.Mode, Cron: r.Cron, Enabled: r.Enabled,
				RcloneOptions:  derefJSON(r.RcloneOptions),
				PreCheckTaskID: r.PreCheckTaskID,
				CreatedAt:      r.CreatedAt, UpdatedAt: r.UpdatedAt,
			}, &c.SyncTasks, "sync_tasks", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("sync_tasks: %w", err)
		}
	}

	// runs.
	if db.Migrator().HasTable("sync_runs") {
		if err := forEach(db, "sync_runs", func(r legacySyncRun) error {
			return insertIfAbsent(db, &database.SyncRun{ID: r.ID}, &database.SyncRun{
				ID: r.ID, TaskID: r.TaskID, JobID: r.JobID, Status: r.Status, Trigger: r.Trigger,
				StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: r.Error,
				Stats: derefJSONNil(r.Stats), NotifiedAt: r.NotifiedAt,
			}, &c.SyncRuns, "sync_runs", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("sync_runs: %w", err)
		}
	}
	if db.Migrator().HasTable("check_runs") {
		if err := forEach(db, "check_runs", func(r legacyCheckRun) error {
			return insertIfAbsent(db, &database.CheckRun{ID: r.ID}, &database.CheckRun{
				ID: r.ID, TaskID: r.TaskID, JobID: r.JobID, Status: r.Status, Trigger: r.Trigger,
				StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: r.Error,
				Result: derefJSONNil(r.Result), NotifiedAt: r.NotifiedAt,
			}, &c.CheckRuns, "check_runs", c.Skipped)
		}); err != nil {
			return c, fmt.Errorf("check_runs: %w", err)
		}
	}

	return c, nil
}

// forEach scans every legacy row into fn (via a throwaway table-mapped type).
func forEach[T any](db *gorm.DB, table string, fn func(T) error) error {
	var rows []T
	if err := db.Table(table).Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// insertIfAbsent writes new when no target row with the same id/key exists.
func insertIfAbsent(db *gorm.DB, probe any, model any, counter *int, table string, skipped map[string]int) error {
	err := db.Where(probe).First(model).Error
	if err == nil {
		skipped[table]++
		return nil
	}
	if err := db.Create(model).Error; err != nil {
		return fmt.Errorf("insert into %s: %w", table, err)
	}
	*counter++
	return nil
}

// remapSide resolves one task side to a data-source id: v2 refs pass
// through; legacy storage refs translate via migration_log.
func remapSide(db *gorm.DB, legacyStorageID, dsID *int64) (int64, bool) {
	if dsID != nil {
		return *dsID, true
	}
	if legacyStorageID == nil {
		return 0, false
	}
	var log database.MigrationLog
	if err := db.First(&log, "table_name = ? AND legacy_id = ?", "storage_config", *legacyStorageID).Error; err != nil {
		return 0, false
	}
	return log.NewID, true
}

func derefJSON(in *database.JSONObject) database.JSONObject {
	if in == nil {
		return database.JSONObject{}
	}
	return *in
}

func derefJSONNil(in *database.JSONObject) *database.JSONObject {
	return in
}
