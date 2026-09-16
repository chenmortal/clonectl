package database

import (
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open connects via GORM. dialect comes from config.ParseDatabaseURL.
func Open(dialect, dsn string) (*gorm.DB, error) {
	var dial gorm.Dialector
	switch dialect {
	case "sqlite":
		dial = sqlite.Open(dsn)
	case "mysql":
		dial = mysql.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported dialect %q", dialect)
	}

	db, err := gorm.Open(dial, &gorm.Config{
		Logger:  logger.Default.LogMode(logger.Warn),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, err
	}

	if dialect == "sqlite" {
		// Single connection serializes writers: the scheduler, elector and
		// API all write from goroutines and SQLITE_BUSY is worse than the
		// negligible throughput cost for this service.
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}
		sqlDB.SetMaxOpenConns(1)
	}
	return db, nil
}

// AutoMigrate creates/updates all tables (create-only semantics for
// existing data, mirroring the Python create_all behavior).
func AutoMigrate(db *gorm.DB) error {
	if err := renameSystemSettingKeyColumn(db); err != nil {
		return err
	}
	return db.AutoMigrate(AllModels()...)
}

// renameSystemSettingKeyColumn renames system_settings_v2.key → setting_key
// ahead of AutoMigrate. The old column name is a reserved word on MySQL
// (any ORDER BY key / WHERE key = ? fails to parse), and GORM would
// otherwise try to ADD the new setting_key primary-key column to existing
// tables, which fails on SQLite and corrupts rows on MySQL.
func renameSystemSettingKeyColumn(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&SystemSetting{}) ||
		!mig.HasColumn(&SystemSetting{}, "key") || // raw name → old column
		mig.HasColumn(&SystemSetting{}, "setting_key") {
		return nil // fresh install, or already renamed (re-run is a no-op)
	}
	if err := mig.RenameColumn(&SystemSetting{}, "key", "setting_key"); err != nil {
		return fmt.Errorf("rename system_settings_v2.key to setting_key: %w", err)
	}
	return nil
}

// NowUTC is the app-wide clock (naive UTC semantics like the Python side).
func NowUTC() time.Time { return time.Now().UTC() }
