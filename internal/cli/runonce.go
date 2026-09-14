package cli

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
	"rclone_sync/internal/services"
)

var runOnceCmd = &cobra.Command{
	Use:   "run-once TASK_ID",
	Short: "立即执行指定任务一次（不经调度器）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		setupLogging(cfg)

		var taskID int64
		if _, err := fmt.Sscanf(args[0], "%d", &taskID); err != nil {
			return fmt.Errorf("invalid task id %q", args[0])
		}

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

		client := rclone.NewClient(cfg.RcloneRCURL, cfg.RcloneRCUser, cfg.RcloneRCPass, 30*time.Second)
		defer client.Close()

		run, err := services.RunTask(db, client, cfg.CheckTimeout, taskID, "manual")
		if err != nil {
			return err
		}
		slog.Info("run finished", "id", run.ID, "status", run.Status, "job_id", run.JobID)
		fmt.Printf("run %d status=%s job_id=%s\n", run.ID, run.Status, intPtrString(run.JobID))
		return nil
	},
}

func intPtrString(v *int64) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprintf("%d", *v)
}

func init() {
	rootCmd.AddCommand(runOnceCmd)
}
