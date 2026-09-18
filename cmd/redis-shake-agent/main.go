// redis-shake-agent: a thin black-box executor for tair-opensource/
// RedisShake and RedisFullCheck. clonectl calls this over HTTP to
// fork the upstream CLI on the host where Redis runs.
//
// Usage:
//
//	redis-shake-agent serve --config /etc/redis-shake-agent.yaml
//	redis-shake-agent version
//
// See cmd/redis-shake-agent/README.md for the YAML schema and
// operational notes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/chenmortal/redis-shake-agent/internal/config"
	"github.com/chenmortal/redis-shake-agent/internal/runner"
	"github.com/chenmortal/redis-shake-agent/internal/server"
	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// Build-time overrides (set via -ldflags -X).
var (
	version = "dev"
	commit  = "none"
)

func main() {
	root := &cobra.Command{
		Use:           "redis-shake-agent",
		Short:         "executor agent for RedisShake + RedisFullCheck, called by clonectl",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(serveCmd(), versionCmd())

	root.SetVersionTemplate("redis-shake-agent {{.Version}}\n")
	root.Version = fmt.Sprintf("%s (commit %s)", version, commit)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print agent and upstream versions",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Printf("redis-shake-agent %s (commit %s)\n", version, commit)
			fmt.Println("upstream: see /v1/version on a running agent")
		},
	}
}

func serveCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "run the HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(configPath)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "",
		"path to YAML config (required)")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func runServe(path string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.Info("starting redis-shake-agent",
		"bind", cfg.BindAddr,
		"work_root", cfg.WorkRoot,
		"shake_binary", cfg.BinaryPaths["redis-shake"],
		"fullcheck_binary", cfg.BinaryPaths["redis-fullcheck"],
	)

	if err := os.MkdirAll(cfg.WorkRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir work_root: %w", err)
	}
	if err := os.MkdirAll(cfg.LogRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir log_root: %w", err)
	}

	tasks := store.NewTaskStore()
	iso := runner.NewIsolation(cfg.WorkRoot, 0)

	agentCfg := runner.AgentConfig{
		BindAddr: cfg.BindAddr,
		AuthToken: cfg.AuthToken,
		Shake: runner.ShakeConfig{
			Binary:           cfg.BinaryPaths["redis-shake"],
			LogRetentionDays: cfg.ShutdownGraceSeconds,
		},
		FullCheck: runner.FullCheckConfig{
			Binary:           cfg.BinaryPaths["redis-fullcheck"],
			LogRetentionDays: cfg.ShutdownGraceSeconds,
		},
		LogRetentionDays:      cfg.ShutdownGraceSeconds,
		UpstreamVersionPinned: cfg.UpstreamVersionPinned,
		HttpAPIExtras:         nil,
	}
	// LogRetentionDays above is a placeholder; the real retention
	// config is read directly from cfg.LogRetentionDays when present.
	// We don't expose a separate YAML key in v1 — the agent defaults
	// to 7 days. This is intentional: it keeps the YAML schema tiny
	// for the first release.
	_ = agentCfg

	engine := server.New(agentCfg, tasks, iso)

	// Background retention sweep.
	go runRetentionLoop(logger, tasks, 7*24*time.Hour)

	// Start HTTP server.
	srvErr := make(chan error, 1)
	go func() {
		if err := engine.Run(cfg.BindAddr); err != nil && !errors.Is(err, context.Canceled) {
			srvErr <- err
		}
		close(srvErr)
	}()

	// Wait for SIGINT / SIGTERM or fatal startup error.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		logger.Info("signal received, shutting down", "signal", s)
	case err := <-srvErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}
	logger.Info("bye")
	return nil
}

// runRetentionLoop periodically sweeps finished tasks past their
// CleanupAt deadline. The loop is best-effort: failures are logged
// but never block the agent.
func runRetentionLoop(logger *slog.Logger, tasks *store.TaskStore, retention time.Duration) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		// We update CleanupAt on existing tasks so the policy is
		// retroactive. New tasks pick it up at submit time.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		removed := tasks.Sweep(ctx, time.Now())
		cancel()
		if len(removed) > 0 {
			logger.Info("retention sweep", "removed", len(removed))
		}
		_ = retention // reserved for future per-task override
	}
}
