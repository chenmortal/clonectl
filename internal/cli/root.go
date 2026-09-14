// Package cli is the cobra command surface (serve/stop/run-once/status/
// migrate-legacy), mirroring the Typer CLI of the Python build.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"rclone_sync/internal/api"
	"rclone_sync/internal/config"
)

var rootCmd = &cobra.Command{
	Use:   "rclone-sync",
	Short: "rclone periodic sync service",
}

// configPath is bound to the persistent --config flag.
var configPath string

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "",
		"配置文件路径（默认读取 ./.env；显式指定但文件不存在时报错）")
}

// SetVersion wires the release version (injected via -ldflags at build time)
// into cobra's built-in --version flag.
func SetVersion(v string) {
	rootCmd.Version = v
	rootCmd.SetVersionTemplate("rclone-sync {{.Version}}\n")
}

// Execute runs the CLI.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// loadConfig reads the --config file when given (missing file is an error),
// otherwise the default .env (missing .env is fine). Real environment
// variables always take precedence over file values.
func loadConfig() (config.Settings, error) {
	if configPath != "" {
		if err := godotenv.Load(configPath); err != nil {
			return config.Settings{}, fmt.Errorf("读取配置文件 %s 失败: %w", configPath, err)
		}
	} else {
		_ = godotenv.Load() // missing .env is fine
	}
	return config.Load(), nil
}

func setupLogging(cfg config.Settings) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{Level: cfg.LogLevelSlog()})))
}

// installSignalHandler shuts the app down on SIGINT/SIGTERM.
func installSignalHandler(app *api.App) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-ch
		slog.Info("received signal, shutting down", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		app.Shutdown(ctx)
		os.Exit(0)
	}()
}
