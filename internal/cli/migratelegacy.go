package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
	"rclone_sync/internal/migrate"
)

var migrateLegacyCmd = &cobra.Command{
	Use:   "migrate-legacy",
	Short: "把旧版（Python）数据库表拷贝到新 *_v2 表（幂等，不改旧表）",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()

		dialect, dsn, err := config.ParseDatabaseURL(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		db, err := database.Open(dialect, dsn)
		if err != nil {
			return err
		}
		sqlDB, _ := db.DB()
		defer sqlDB.Close()

		// Targets must exist before copying.
		if err := database.AutoMigrate(db); err != nil {
			return err
		}

		counts, err := migrate.Run(db)
		if err != nil {
			return err
		}
		fmt.Println("migrate-legacy done:")
		fmt.Printf("  users:                 %d (skipped %d)\n", counts.Users, counts.Skipped["users"])
		fmt.Printf("  storage_sources:       %d (skipped %d)\n", counts.StorageSources, counts.Skipped["storage_sources"])
		fmt.Printf("  data_sources:          %d (skipped %d)\n", counts.DataSources, counts.Skipped["data_sources"])
		fmt.Printf("  bindings:              %d (skipped %d)\n", counts.Bindings, counts.Skipped["data_source_bindings"])
		fmt.Printf("  system_settings:       %d (skipped %d)\n", counts.Settings, counts.Skipped["system_settings"])
		fmt.Printf("  check_tasks:           %d (skipped %d unmapped)\n", counts.CheckTasks, counts.CheckTasksSkipped)
		fmt.Printf("  sync_tasks:            %d (skipped %d unmapped)\n", counts.SyncTasks, counts.SyncTasksSkipped)
		fmt.Printf("  sync_runs:             %d (skipped %d)\n", counts.SyncRuns, counts.Skipped["sync_runs"])
		fmt.Printf("  check_runs:            %d (skipped %d)\n", counts.CheckRuns, counts.Skipped["check_runs"])
		fmt.Printf("  storage_configs fan-out: %d sources, %d data sources created\n",
			counts.SourcesCreated, counts.DatasourcesCreated)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateLegacyCmd)
}
