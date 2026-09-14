// Package cli is the cobra command surface (serve/stop/run-once/status/
// migrate-legacy), mirroring the Typer CLI of the Python build.
package cli

import (
	"context"
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

// Execute runs the CLI.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadConfig() config.Settings {
	_ = godotenv.Load() // missing .env is fine
	return config.Load()
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
