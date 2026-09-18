package services

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"clonectl/internal/database"
	"clonectl/internal/rclone"
)

// Startup auto-migration: legacy storage_configs → storage source + default
// data source, guarded by migration_log for idempotency. Only touches the
// fan-out step — legacy task storage-id refs don't exist in the *_v2 task
// tables (the migrate-legacy CLI remaps those during the one-off copy).

var (
	sourceOnlyKeys = map[string]bool{"endpoint": true, "region": true, "name": true}
	secretKeys     = map[string]bool{"access_key_id": true, "secret_access_key": true, "token": true}
)

// splitParameters → (extra for StorageSource.extra, secrets for DataSource).
func splitParameters(params database.JSONObject) (extra, secrets database.JSONObject) {
	extra = database.JSONObject{}
	secrets = database.JSONObject{}
	for k, v := range params {
		if sourceOnlyKeys[k] {
			continue
		}
		if secretKeys[k] {
			secrets[k] = v
			continue
		}
		extra[k] = v
	}
	return extra, secrets
}

// HasLegacyStorageConfigs reports whether the legacy table exists with rows.
func HasLegacyStorageConfigs(db *gorm.DB) bool {
	if !db.Migrator().HasTable(&database.StorageConfig{}) {
		return false
	}
	var count int64
	if err := db.Model(&database.StorageConfig{}).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// LegacyMigrationCounts reports what the startup migration did (for logging).
type LegacyMigrationCounts struct {
	SourcesCreated         int
	DatasourcesCreated     int
	SkippedAlreadyMigrated int
	SkippedNoAdmin         int
}

// MigrateLegacyStorageConfigs copies each unmigrated storage_config row into
// storage_sources_v2 + a "-default" data source owned by the lowest-id admin.
func MigrateLegacyStorageConfigs(db *gorm.DB) (LegacyMigrationCounts, error) {
	counts := LegacyMigrationCounts{}

	var configs []database.StorageConfig
	if err := db.Order("id").Find(&configs).Error; err != nil {
		return counts, err
	}
	if len(configs) == 0 {
		return counts, nil
	}

	// DataSource.owner_user_id is NOT NULL — refuse without an admin.
	var owner database.User
	if err := db.Where("role = ?", database.RoleAdmin).Order("id").First(&owner).Error; err != nil {
		slog.Error("storage migration skipped: no admin user to own migrated data sources")
		counts.SkippedNoAdmin = len(configs)
		return counts, nil
	}

	for _, cfg := range configs {
		var log database.MigrationLog
		err := db.First(&log, "table_name = ? AND legacy_id = ?", "storage_config", cfg.ID).Error
		if err == nil {
			counts.SkippedAlreadyMigrated++
			continue
		}

		params := cfg.Parameters
		extra, secrets := splitParameters(params)

		src := database.StorageSource{
			Name: cfg.Name, Type: cfg.Type, Extra: extra,
		}
		if v, ok := params["endpoint"].(string); ok {
			src.Endpoint = &v
		}
		if v, ok := params["region"].(string); ok {
			src.Region = &v
		}
		if err := db.Create(&src).Error; err != nil {
			return counts, fmt.Errorf("create source %s: %w", cfg.Name, err)
		}

		ds := database.DataSource{
			Name:            fmt.Sprintf("%s-default", cfg.Name),
			StorageSourceID: src.ID,
			Path:            "/",
			Description:     strPtr(fmt.Sprintf("migrated from storage_config %d", cfg.ID)),
			OwnerUserID:     owner.ID,
		}
		if v, ok := params["path"].(string); ok && v != "" {
			ds.Path = v
		}
		if v, ok := secrets["access_key_id"].(string); ok && v != "" {
			ds.AccessKeyID = &v
		}
		if v, ok := secrets["secret_access_key"].(string); ok && v != "" {
			ds.SecretAccessKey = &v
		}
		if err := db.Create(&ds).Error; err != nil {
			return counts, fmt.Errorf("create data source %s: %w", ds.Name, err)
		}

		if err := db.Create(&database.MigrationLog{
			LegacyTable: "storage_config", LegacyID: cfg.ID, NewID: ds.ID,
		}).Error; err != nil {
			return counts, err
		}
		counts.SourcesCreated++
		counts.DatasourcesCreated++
	}
	return counts, nil
}

// SyncAllRemotes pushes every legacy storage_config as an rclone remote
// (startup recovery for rcd restarts). Returns (synced, failed).
func SyncAllRemotes(db *gorm.DB, client *rclone.Client) (int, int) {
	var configs []database.StorageConfig
	if err := db.Order("id").Find(&configs).Error; err != nil {
		slog.Error("sync_all_remotes: list failed", "err", err)
		return 0, 0
	}
	synced, failed := 0, 0
	for _, cfg := range configs {
		if err := EnsureRemote(db, client, &cfg); err != nil {
			slog.Error("sync_all_remotes: push failed", "name", cfg.Name, "err", err)
			failed++
			continue
		}
		synced++
	}
	return synced, failed
}
