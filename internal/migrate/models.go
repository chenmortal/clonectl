package migrate

import (
	"time"

	"clonectl/internal/database"
)

// Legacy-schema read models (column-for-column with the Python tables;
// never AutoMigrated, never written).

type legacyUser struct {
	ID           int64      `gorm:"primaryKey;column:id"`
	Username     string     `gorm:"column:username"`
	PasswordHash string     `gorm:"column:password_hash"`
	Role         string     `gorm:"column:role"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	LastLoginAt  *time.Time `gorm:"column:last_login_at"`
	DisabledAt   *time.Time `gorm:"column:disabled_at"`
}

type legacyStorageSource struct {
	ID        int64                `gorm:"primaryKey;column:id"`
	Name      string               `gorm:"column:name"`
	Type      string               `gorm:"column:type"`
	Endpoint  *string              `gorm:"column:endpoint"`
	Region    *string              `gorm:"column:region"`
	Extra     *database.JSONObject `gorm:"column:extra"`
	CreatedAt time.Time            `gorm:"column:created_at"`
	UpdatedAt time.Time            `gorm:"column:updated_at"`
}

type legacyDataSource struct {
	ID              int64      `gorm:"primaryKey;column:id"`
	Name            string     `gorm:"column:name"`
	StorageSourceID int64      `gorm:"column:storage_source_id"`
	Path            string     `gorm:"column:path"`
	AccessKeyID     *string    `gorm:"column:access_key_id"`
	SecretAccessKey *string    `gorm:"column:secret_access_key"`
	Description     *string    `gorm:"column:description"`
	OwnerUserID     int64      `gorm:"column:owner_user_id"`
	LastVerifiedAt  *time.Time `gorm:"column:last_verified_at"`
	LastVerifiedOK  *bool      `gorm:"column:last_verified_ok"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

type legacyBinding struct {
	ID              int64     `gorm:"primaryKey;column:id"`
	DataSourceID    int64     `gorm:"column:data_source_id"`
	UserID          int64     `gorm:"column:user_id"`
	Permission      string    `gorm:"column:permission"`
	CreatedByUserID *int64    `gorm:"column:created_by_user_id"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

type legacySetting struct {
	Key             string    `gorm:"primaryKey;column:key"`
	Value           string    `gorm:"column:value"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	UpdatedByUserID *int64    `gorm:"column:updated_by_user_id"`
}

type legacySyncTask struct {
	ID              int64                `gorm:"primaryKey;column:id"`
	Name            string               `gorm:"column:name"`
	SrcStorageID    *int64               `gorm:"column:src_storage_id"`
	SrcPath         string               `gorm:"column:src_path"`
	DstStorageID    *int64               `gorm:"column:dst_storage_id"`
	DstPath         string               `gorm:"column:dst_path"`
	SrcDataSourceID *int64               `gorm:"column:src_data_source_id"`
	DstDataSourceID *int64               `gorm:"column:dst_data_source_id"`
	Mode            string               `gorm:"column:mode"`
	Cron            string               `gorm:"column:cron"`
	Enabled         bool                 `gorm:"column:enabled"`
	RcloneOptions   *database.JSONObject `gorm:"column:rclone_options"`
	PreCheckTaskID  *int64               `gorm:"column:pre_check_task_id"`
	CreatedAt       time.Time            `gorm:"column:created_at"`
	UpdatedAt       time.Time            `gorm:"column:updated_at"`
}

type legacyCheckTask struct {
	ID              int64                `gorm:"primaryKey;column:id"`
	Name            string               `gorm:"column:name"`
	SrcStorageID    *int64               `gorm:"column:src_storage_id"`
	SrcPath         string               `gorm:"column:src_path"`
	DstStorageID    *int64               `gorm:"column:dst_storage_id"`
	DstPath         string               `gorm:"column:dst_path"`
	SrcDataSourceID *int64               `gorm:"column:src_data_source_id"`
	DstDataSourceID *int64               `gorm:"column:dst_data_source_id"`
	Cron            *string              `gorm:"column:cron"`
	Enabled         bool                 `gorm:"column:enabled"`
	CheckOptions    *database.JSONObject `gorm:"column:check_options"`
	CreatedAt       time.Time            `gorm:"column:created_at"`
	UpdatedAt       time.Time            `gorm:"column:updated_at"`
}

type legacySyncRun struct {
	ID         int64                `gorm:"primaryKey;column:id"`
	TaskID     int64                `gorm:"column:task_id"`
	JobID      *int64               `gorm:"column:job_id"`
	Status     string               `gorm:"column:status"`
	Trigger    string               `gorm:"column:trigger"`
	StartedAt  *time.Time           `gorm:"column:started_at"`
	FinishedAt *time.Time           `gorm:"column:finished_at"`
	Error      *string              `gorm:"column:error"`
	Stats      *database.JSONObject `gorm:"column:stats"`
	NotifiedAt *time.Time           `gorm:"column:notified_at"`
}

type legacyCheckRun struct {
	ID         int64                `gorm:"primaryKey;column:id"`
	TaskID     int64                `gorm:"column:task_id"`
	JobID      *int64               `gorm:"column:job_id"`
	Status     string               `gorm:"column:status"`
	Trigger    string               `gorm:"column:trigger"`
	StartedAt  *time.Time           `gorm:"column:started_at"`
	FinishedAt *time.Time           `gorm:"column:finished_at"`
	Error      *string              `gorm:"column:error"`
	Result     *database.JSONObject `gorm:"column:result"`
	NotifiedAt *time.Time           `gorm:"column:notified_at"`
}
