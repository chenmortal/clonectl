package cli

import (
	"github.com/spf13/cobra"

	"rclone_sync/internal/api"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 API + gocron 调度器（自动初始化数据库表）",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		setupLogging(cfg)
		app := api.NewApp(cfg)
		installSignalHandler(app)
		return app.Start()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
