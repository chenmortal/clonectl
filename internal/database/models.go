// Package database holds the redesigned GORM schema. Business tables carry
// a _v2 suffix so the legacy Python tables stay untouched (rollback safety);
// cluster/lease/migration_log/storage_configs keep their legacy names because
// HA nodes must contend on the same rows and the legacy read API persists.
package database

import (
	"time"
)

// --- enums (string columns + Go constants; validated at the API layer) ---

const (
	RoleAdmin = "admin"
	RoleEdit  = "edit"
	RoleView  = "view"
)

var Roles = []string{RoleAdmin, RoleEdit, RoleView}

const (
	ModeSync = "sync"
	ModeCopy = "copy"
)

const (
	PermissionRead  = "read"
	PermissionWrite = "write"
	PermissionAdmin = "admin"
)

var PermissionRank = map[string]int{PermissionRead: 1, PermissionWrite: 2, PermissionAdmin: 3}

const (
	RunPending = "pending"
	RunRunning = "running"
	RunSuccess = "success"
	RunFailed  = "failed"
	RunSkipped = "skipped"
)

const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

const (
	SettingAlertmanagerURL = "alertmanager_url"
	SettingSiteTitle       = "site_title"
)

// --- auth ---

type User struct {
	ID           int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string     `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:255;not null" json:"-"`
	Role         string     `gorm:"size:16;not null" json:"role"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	DisabledAt   *time.Time `json:"disabled_at"`
}

func (User) TableName() string { return "users_v2" }

// --- storage sources / data sources ---

type StorageSource struct {
	ID        int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string     `gorm:"size:128;uniqueIndex;not null" json:"name"`
	Type      string     `gorm:"size:64;not null" json:"type"`
	Endpoint  *string    `gorm:"size:512" json:"endpoint"`
	Region    *string    `gorm:"size:64" json:"region"`
	Path      *string    `gorm:"size:512" json:"path"` // local backend FS root prefix
	Extra     JSONObject `gorm:"not null" json:"extra"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (StorageSource) TableName() string { return "storage_sources_v2" }

type DataSource struct {
	ID              int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name            string     `gorm:"size:128;not null;uniqueIndex:idx_ds_owner_name,priority:2" json:"name"`
	StorageSourceID int64      `gorm:"not null;index" json:"storage_source_id"`
	Path            string     `gorm:"size:512;not null" json:"path"`
	AccessKeyID     *string    `gorm:"size:255" json:"access_key_id"`
	SecretAccessKey *string    `gorm:"size:512" json:"secret_access_key"`
	Description     *string    `gorm:"size:512" json:"description"`
	OwnerUserID     int64      `gorm:"not null;index;uniqueIndex:idx_ds_owner_name,priority:1" json:"owner_user_id"`
	LastVerifiedAt  *time.Time `json:"last_verified_at"`
	LastVerifiedOK  *bool      `json:"last_verified_ok"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (DataSource) TableName() string { return "data_sources_v2" }

type DataSourceBinding struct {
	ID              int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	DataSourceID    int64     `gorm:"not null;index;uniqueIndex:idx_binding_ds_user,priority:1" json:"data_source_id"`
	UserID          int64     `gorm:"not null;index;uniqueIndex:idx_binding_ds_user,priority:2" json:"user_id"`
	Permission      string    `gorm:"size:16;not null" json:"permission"`
	CreatedByUserID *int64    `json:"created_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

func (DataSourceBinding) TableName() string { return "data_source_bindings_v2" }

// --- system settings ---

type SystemSetting struct {
	Key             string    `gorm:"column:setting_key;size:128;primaryKey" json:"key"` // column renamed: `key` is a MySQL reserved word
	Value           string    `gorm:"not null" json:"value"`
	UpdatedAt       time.Time `json:"updated_at"`
	UpdatedByUserID *int64    `json:"updated_by_user_id"`
}

func (SystemSetting) TableName() string { return "system_settings_v2" }

// --- tasks ---

type SyncTask struct {
	ID              int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name            string     `gorm:"size:128;not null" json:"name"`
	SrcDataSourceID int64      `gorm:"not null;index" json:"src_data_source_id"`
	SrcPath         string     `gorm:"size:512;not null" json:"src_path"`
	DstDataSourceID int64      `gorm:"not null;index" json:"dst_data_source_id"`
	DstPath         string     `gorm:"size:512;not null" json:"dst_path"`
	Mode            string     `gorm:"size:16;not null" json:"mode"`
	Cron            string     `gorm:"size:128;not null" json:"cron"`
	Enabled         bool       `gorm:"not null" json:"enabled"`
	RcloneOptions   JSONObject `gorm:"not null" json:"rclone_options"`
	PreCheckTaskID  *int64     `json:"pre_check_task_id"`
	// CreatorUserID is recorded on creation but does NOT grant access by itself.
	// Access is governed by sync_task_bindings_v2 (a default admin row is
	// inserted for the creator at create time and may be revoked).
	// default:0 keeps SQLite's ALTER TABLE ADD NOT NULL happy for pre-existing
	// rows in dev DBs; the value is purely informational.
	CreatorUserID  int64     `gorm:"not null;default:0;index" json:"creator_user_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (SyncTask) TableName() string { return "sync_tasks_v2" }

// SyncTaskBinding mirrors DataSourceBinding for sync tasks. A row is created
// at task-creation time with PermissionAdmin for the creator; that row is the
// only thing granting access to non-global-admin callers.
type SyncTaskBinding struct {
	ID              int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	SyncTaskID      int64     `gorm:"not null;index;uniqueIndex:idx_stb_task_user,priority:1" json:"sync_task_id"`
	UserID          int64     `gorm:"not null;index;uniqueIndex:idx_stb_task_user,priority:2" json:"user_id"`
	Permission      string    `gorm:"size:16;not null" json:"permission"`
	CreatedByUserID *int64    `json:"created_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

func (SyncTaskBinding) TableName() string { return "sync_task_bindings_v2" }

type CheckTask struct {
	ID              int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name            string     `gorm:"size:128;not null" json:"name"`
	SrcDataSourceID int64      `gorm:"not null;index" json:"src_data_source_id"`
	SrcPath         string     `gorm:"size:512;not null" json:"src_path"`
	DstDataSourceID int64      `gorm:"not null;index" json="dst_data_source_id"`
	DstPath         string     `gorm:"size:512;not null" json:"dst_path"`
	Cron            *string    `gorm:"size:128" json:"cron"`
	Enabled         bool       `gorm:"not null" json:"enabled"`
	CheckOptions    JSONObject `gorm:"not null" json:"check_options"`
	// CreatorUserID is recorded on creation; access is governed by
	// check_task_bindings_v2 (default admin row for the creator).
	// default:0 keeps SQLite's ALTER TABLE ADD NOT NULL happy for pre-existing
	// rows; the value is purely informational.
	CreatorUserID int64     `gorm:"not null;default:0;index" json:"creator_user_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (CheckTask) TableName() string { return "check_tasks_v2" }

// CheckTaskBinding mirrors SyncTaskBinding for check tasks.
type CheckTaskBinding struct {
	ID              int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CheckTaskID     int64     `gorm:"not null;index;uniqueIndex:idx_ctb_task_user,priority:1" json:"check_task_id"`
	UserID          int64     `gorm:"not null;index;uniqueIndex:idx_ctb_task_user,priority:2" json:"user_id"`
	Permission      string    `gorm:"size:16;not null" json:"permission"`
	CreatedByUserID *int64    `json:"created_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

func (CheckTaskBinding) TableName() string { return "check_task_bindings_v2" }

// --- runs ---

type SyncRun struct {
	ID         int64       `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID     int64       `gorm:"not null;index:idx_sync_runs_task_status,priority:1" json:"task_id"`
	JobID      *int64      `json:"job_id"`
	Status     string      `gorm:"size:16;not null;default:pending;index:idx_sync_runs_task_status,priority:2;index" json:"status"`
	Trigger    string      `gorm:"size:16;not null" json:"trigger"`
	StartedAt  *time.Time  `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at"`
	Error      *string     `gorm:"type:text" json:"error"`
	Stats      *JSONObject `json:"stats"`
	NotifiedAt *time.Time  `json:"-"`
}

func (SyncRun) TableName() string { return "sync_runs_v2" }

type CheckRun struct {
	ID         int64       `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskID     int64       `gorm:"not null;index:idx_check_runs_task_status,priority:1" json:"task_id"`
	JobID      *int64      `json:"job_id"`
	Status     string      `gorm:"size:16;not null;default:pending;index:idx_check_runs_task_status,priority:2;index" json:"status"`
	Trigger    string      `gorm:"size:16;not null" json:"trigger"`
	StartedAt  *time.Time  `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at"`
	Error      *string     `gorm:"type:text" json:"error"`
	Result     *JSONObject `json:"result"`
	NotifiedAt *time.Time  `json:"-"`
}

func (CheckRun) TableName() string { return "check_runs_v2" }

// --- cluster / leader election (legacy table names, shared across nodes) ---

type ClusterNode struct {
	NodeID        string     `gorm:"size:64;primaryKey" json:"node_id"`
	ClusterName   string     `gorm:"size:64;not null;index" json:"cluster_name"`
	Role          string     `gorm:"size:16;not null" json:"role"`
	Hostname      *string    `gorm:"size:255" json:"hostname"`
	IPAddress     *string    `gorm:"size:45" json:"ip_address"`
	StartedAt     time.Time  `json:"started_at"`
	LastHeartbeat time.Time  `json:"last_heartbeat"`
	LeftAt        *time.Time `json:"left_at"`
}

func (ClusterNode) TableName() string { return "cluster_nodes" }

type LeaderLease struct {
	ClusterName  string     `gorm:"size:64;primaryKey" json:"cluster_name"`
	LeaderNodeID *string    `gorm:"size:64" json:"leader_node_id"`
	LeaseUntil   *time.Time `json:"lease_until"`
	Epoch        int64      `gorm:"not null;default:0" json:"epoch"`
}

func (LeaderLease) TableName() string { return "leader_lease" }

// --- legacy tables (kept for rollback safety and read compat) ---

// StorageConfig is the legacy storage_configs table: read-only via the API,
// still writable by the old Python build, and the source of the startup
// migration into storage sources + data sources.
type StorageConfig struct {
	ID         int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name       string     `gorm:"size:128;uniqueIndex;not null" json:"name"`
	Type       string     `gorm:"size:64;not null" json:"type"`
	Parameters JSONObject `gorm:"not null" json:"parameters"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (StorageConfig) TableName() string { return "storage_configs" }

// MigrationLog records legacy-row → new-row mappings (idempotency guard).
type MigrationLog struct {
	LegacyTable string    `gorm:"column:table_name;size:64;primaryKey:1" json:"table_name"`
	LegacyID    int64     `gorm:"primaryKey:2" json:"legacy_id"`
	NewID       int64     `gorm:"not null" json:"new_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func (MigrationLog) TableName() string { return "migration_log" }

// AllModels is the AutoMigrate list.
func AllModels() []any {
	return []any{
		&User{},
		&StorageSource{},
		&DataSource{},
		&DataSourceBinding{},
		&SystemSetting{},
		&SyncTask{},
		&CheckTask{},
		&SyncTaskBinding{},
		&CheckTaskBinding{},
		&SyncRun{},
		&CheckRun{},
		&ClusterNode{},
		&LeaderLease{},
		&StorageConfig{},
		&MigrationLog{},
	}
}
