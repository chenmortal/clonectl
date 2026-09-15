// Package services holds the business layer: storage remotes, run engine,
// checks and alerting.
package services

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
)

// DSRemoteName is the deterministic rclone-side remote name for a DataSource.
func DSRemoteName(ds *database.DataSource) string { return fmt.Sprintf("ds-%d", ds.ID) }

// BuildRemoteParameters builds the rclone parameters dict for a
// (storage_source, data_source) pair sent to /config/create.
//
// type="local" omits credentials (rclone ignores them; pushing AK/SK would
// just leak values into the rcd config) and defaults extra.root="/" so
// ds-N:/abs/path resolves to the literal host path instead of CWD-relative.
func BuildRemoteParameters(src *database.StorageSource, ds *database.DataSource) database.JSONObject {
	params := src.Extra.Clone()
	if src.Endpoint != nil {
		params["endpoint"] = *src.Endpoint
	}
	if src.Region != nil {
		params["region"] = *src.Region
	}
	if src.Type == "local" {
		if _, ok := params["root"]; !ok {
			params["root"] = "/"
		}
		return params
	}
	if src.Type == "s3" {
		// rclone S3 requires an explicit provider unless endpoint is set.
		if _, ok := params["provider"]; !ok {
			params["provider"] = "Other"
		}
	}
	params["access_key_id"] = strOrEmpty(ds.AccessKeyID)
	params["secret_access_key"] = strOrEmpty(ds.SecretAccessKey)
	return params
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// EnsureRemote pushes one legacy StorageConfig remote, swallowing
// "already exists" errors (idempotent).
func EnsureRemote(db *gorm.DB, client *rclone.Client, cfg *database.StorageConfig) error {
	_, err := client.CreateRemote(cfg.Name, cfg.Type, cfg.Parameters)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	return err
}

// EnsureDataSourceRemote pushes the ds-{id} remote for a DataSource.
func EnsureDataSourceRemote(db *gorm.DB, client *rclone.Client, ds *database.DataSource) error {
	var src database.StorageSource
	if err := db.First(&src, ds.StorageSourceID).Error; err != nil {
		return fmt.Errorf("storage source %d not found: %w", ds.StorageSourceID, err)
	}
	_, err := client.CreateRemote(DSRemoteName(ds), src.Type, BuildRemoteParameters(&src, ds))
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	return err
}

// RemoveRemote removed: legacy storage_configs write path is gone (the
// /api/storages writes 410 Gone), and no handler calls this anymore.

