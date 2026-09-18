// Package services holds the business layer: storage remotes, run engine,
// checks and alerting.
package services

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"clonectl/internal/database"
	"clonectl/internal/rclone"
)

// DSRemoteName is the deterministic rclone-side remote name for a DataSource.
func DSRemoteName(ds *database.DataSource) string { return fmt.Sprintf("ds-%d", ds.ID) }

// SidePath composes the path that follows "ds-N:" in an rclone fs spec
// for a data source. Local storage sources bake their FS prefix into the
// path itself: rclone's local backend has no "root" config option (a
// root parameter is stored but silently ignored — verified against
// rclone v1.74), so without baking, ds-N:/abs resolves under the rcd
// working directory instead of the prefix. The manager launches rcd with
// CWD=/ so the baked path is absolute by contract. Other backends (s3:
// the bucket/prefix lives in the data source path) use it as-is. Prefix
// fallback order: path column → extra.root → "/".
func SidePath(src *database.StorageSource, dsPath string) string {
	if src.Type != "local" {
		return dsPath
	}
	return JoinDSPath(localFSRoot(src), dsPath)
}

func localFSRoot(src *database.StorageSource) string {
	if src.Path != nil && *src.Path != "" {
		return *src.Path
	}
	if r, ok := src.Extra["root"].(string); ok && r != "" {
		return r
	}
	return "/"
}

// BuildRemoteParameters builds the rclone parameters dict for a
// (storage_source, data_source) pair sent to /config/create.
//
// type="local": no credentials (rclone ignores them; pushing AK/SK would
// leak values into the rcd config) and no endpoint. The storage source's
// path prefix is NOT sent here — the local backend has no "root" config
// option; it is baked into fs paths at resolution time (see SidePath).
//
// type="s3": provider comes from extra (required, validated at the API
// layer; "Other" kept as a legacy fallback for rows created before the
// constraint existed). list_version defaults to "1" — the classic
// ListObjects — because rclone's provider-based auto mode picks
// ListObjectsV2 for old OSS builds that don't implement it; a list_version
// in extra wins.
//
// type="redis": critical decoupling point. rclone does NOT own Redis
// connections — the redis-shake-agent does. We return an EMPTY params
// dict and rely on the runner to never send /config/create for redis
// sources (the redis path uses agent.Submit, not rcd). This function
// exists for the StorageSource row but is intentionally a no-op for
// the redis branch so future readers understand the boundary.
func BuildRemoteParameters(src *database.StorageSource, ds *database.DataSource) database.JSONObject {
	if src.Type == "redis" {
		// No rclone remote is registered for redis sources; the
		// redis-shake-agent talks to Redis directly. Return empty.
		return database.JSONObject{}
	}

	params := src.Extra.Clone()
	if src.Endpoint != nil {
		params["endpoint"] = *src.Endpoint
	}
	if src.Region != nil {
		params["region"] = *src.Region
	}
	if src.Type == "local" {
		return params
	}
	if src.Type == "s3" {
		// rclone S3 requires an explicit provider unless endpoint is set.
		if _, ok := params["provider"]; !ok {
			params["provider"] = "Other"
		}
		if _, ok := params["list_version"]; !ok {
			params["list_version"] = "1"
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
