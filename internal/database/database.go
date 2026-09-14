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
	return db.AutoMigrate(AllModels()...)
}

// NowUTC is the app-wide clock (naive UTC semantics like the Python side).
func NowUTC() time.Time { return time.Now().UTC() }
