package cli

import (
	"fmt"
	"net"
	"strconv"

	"github.com/spf13/cobra"

	"rclone_sync/internal/api"
)

var bindAddr string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 API + gocron 调度器（自动初始化数据库表）",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if bindAddr != "" {
			host, port, perr := parseBind(bindAddr)
			if perr != nil {
				return perr
			}
			if host != "" {
				cfg.APIHost = host
			}
			cfg.APIPort = port
		}
		setupLogging(cfg)
		app := api.NewApp(cfg)
		installSignalHandler(app)
		return app.Start()
	},
}

// parseBind accepts "host:port", ":port" (host left as configured) or a bare
// port number "8000" (binds all interfaces).
func parseBind(bind string) (host string, port int, err error) {
	h, p, splitErr := net.SplitHostPort(bind)
	if splitErr == nil {
		port, err = strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("无效的 --bind 端口 %q: %w", p, err)
		}
		return h, port, nil
	}
	// Bare port?
	if n, aerr := strconv.Atoi(bind); aerr == nil {
		return "", n, nil
	}
	return "", 0, fmt.Errorf("无效的 --bind %q（应为 host:port、:port 或纯端口）", bind)
}

func init() {
	serveCmd.Flags().StringVarP(&bindAddr, "bind", "b", "",
		"覆盖监听地址（host:port / :port / 纯端口；默认取 API_HOST:API_PORT）")
	rootCmd.AddCommand(serveCmd)
}
