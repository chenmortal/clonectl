package database

import (
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if err := precreateCreatorUserIDColumns(db); err != nil {
		return err
	}
	// Skip SyncTask / CheckTask ONLY when their table already exists:
	// precreateCreatorUserIDColumns added creator_user_id for us, and
	// letting GORM re-compare the schema triggers a recreateTable that
	// drops rows on SQLite. For fresh installs / migrations the table
	// does not exist yet, so we keep them in the AutoMigrate list and
	// let GORM create it normally.
	return db.AutoMigrate(filteredModels(func(t any) bool {
		switch t.(type) {
		case *SyncTask, *CheckTask:
			return !db.Migrator().HasTable(t)
		}
		return true
	})...)
}

// filteredModels returns the AllModels list with entries that fn rejects.
func filteredModels(keep func(any) bool) []any {
	all := AllModels()
	out := make([]any, 0, len(all))
	for _, m := range all {
		if keep(m) {
			out = append(out, m)
		}
	}
	return out
}

// precreateCreatorUserIDColumns brings sync_tasks_v2 / check_tasks_v2 to a
// shape that has the new creator_user_id column BEFORE GORM's AutoMigrate
// runs. GORM's SQLite migrator adds a NOT NULL column by rebuilding the
// table (recreateTable), which fails on existing rows; doing a plain
// ALTER TABLE ADD COLUMN ... DEFAULT 0 instead is a no-op on MySQL (the
// guard on HasTable + !HasColumn short-circuits) and safe on SQLite for
// both fresh and existing schemas. We then exclude those two models from
// the AutoMigrate call so GORM's type-comparison pass doesn't try to
// recreate the table again.
func precreateCreatorUserIDColumns(db *gorm.DB) error {
	mig := db.Migrator()
	for _, target := range []any{&SyncTask{}, &CheckTask{}} {
		hasTable := mig.HasTable(target)
		if hasTable && mig.HasColumn(target, "creator_user_id") {
			continue
		}
		if !hasTable {
			// Fresh install — let GORM CreateTable handle it from AllModels.
			continue
		}
		// DEFAULT 0 satisfies the NOT NULL constraint for legacy rows.
		// (DEFAULT NULL would fail on SQLite's "Cannot add a NOT NULL
		// column with default value NULL" check.)
		if err := db.Exec(
			"ALTER TABLE ? ADD COLUMN creator_user_id INTEGER NOT NULL DEFAULT 0",
			clause.Table{Name: targetTableName(target)},
		).Error; err != nil {
			return fmt.Errorf("add creator_user_id to %T: %w", target, err)
		}
	}
	return nil
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

// targetTableName returns the physical table name for a *gorm.Model. Used
// only by the manual ALTER TABLE path above.
func targetTableName(m any) string {
	type tabler interface{ TableName() string }
	if t, ok := m.(tabler); ok {
		return t.TableName()
	}
	return ""
}
