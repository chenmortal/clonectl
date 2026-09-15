package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	rclone_sync "rclone_sync"
	"rclone_sync/internal/auth"
	"rclone_sync/internal/cluster"
	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
	"rclone_sync/internal/scheduler"
	"rclone_sync/internal/services"
)

// App owns the whole server lifecycle (startup order mirrors app/main.py).
type App struct {
	Cfg      config.Settings
	DB       *gorm.DB
	Deps     *Deps
	RC       *rclone.Client
	Manager  *rclone.Manager
	Sched    *scheduler.Service
	Elector  *cluster.LeaderElector
	srv      *http.Server
	rootDirs struct{ static string } // resolved STATIC_DIR (dev override)
}

// NewApp builds an unstarted App.
func NewApp(cfg config.Settings) *App { return &App{Cfg: cfg} }

// Start runs the startup sequence and begins serving. It blocks until the
// HTTP server exits; call Shutdown (from a signal handler goroutine) to
// stop gracefully.
func (a *App) Start() error {
	cfg := a.Cfg

	// 1. JWT secret validation (empty = fatal; placeholder = warning).
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.JWTSecret == "CHANGE-ME-IN-PRODUCTION" {
		slog.Warn("JWT_SECRET is the built-in placeholder; set JWT_SECRET env var (openssl rand -hex 32)")
	}

	// 2. Database.
	dialect, dsn, err := config.ParseDatabaseURL(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	db, err := database.Open(dialect, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if err := database.AutoMigrate(db); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	a.DB = db

	// 3. PID file.
	if err := os.WriteFile(cfg.PIDFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		slog.Error("write pid file failed", "err", err)
	}

	// 4. Managed rclone rcd.
	var manager *rclone.Manager
	if cfg.RcloneManaged {
		manager = rclone.NewManager(
			rclone.RCNormal(cfg.RcloneRCURL), cfg.RcloneRCAddr,
			cfg.RcloneRCUser, cfg.RcloneRCPass, cfg.RcloneBin)
		manager.WebGUI = cfg.RcloneWebGUI
		if err := manager.Start(30 * time.Second); err != nil {
			return err
		}
	}
	a.Manager = manager

	// 5. rclone client + proxy.
	rc := rclone.NewClient(cfg.RcloneRCURL, cfg.RcloneRCUser, cfg.RcloneRCPass, 30*time.Second)
	a.RC = rc
	var proxy *httputil.ReverseProxy
	{
		u, perr := url.Parse(cfg.RcloneRCURL)
		if perr != nil {
			return fmt.Errorf("RCLONE_RC_URL: %w", perr)
		}
		proxy = NewRcloneProxy(u, cfg.RcloneRCUser, cfg.RcloneRCPass)
	}

	// 6. Startup legacy migration (only when legacy rows exist).
	if services.HasLegacyStorageConfigs(db) {
		counts, err := services.MigrateLegacyStorageConfigs(db)
		if err != nil {
			slog.Error("legacy storage migration failed (continuing)", "err", err)
		} else {
			slog.Info("legacy storage migration",
				"sources", counts.SourcesCreated, "datasources", counts.DatasourcesCreated,
				"skipped", counts.SkippedAlreadyMigrated, "no_admin", counts.SkippedNoAdmin)
		}
	}

	// 7. Seed alertmanager_url system setting from env (row-absent only).
	if cfg.AlertmanagerURL != "" {
		var n int64
		db.Model(&database.SystemSetting{}).Where("key = ?", database.SettingAlertmanagerURL).Count(&n)
		if n == 0 {
			if err := db.Create(&database.SystemSetting{
				Key: database.SettingAlertmanagerURL, Value: cfg.AlertmanagerURL,
			}).Error; err != nil {
				slog.Error("seed alertmanager_url failed", "err", err)
			}
		}
	}

	// 8. Push legacy remotes (rcd restart recovery).
	if synced, failed := services.SyncAllRemotes(db, rc); synced > 0 || failed > 0 {
		slog.Info("sync_all_remotes", "synced", synced, "failed", failed)
	}

	// 9. Scheduler (leader-gated user jobs).
	sched, err := scheduler.New(db, rc, cfg,
		func(taskID int64) *int64 {
			run, err := services.RunTask(db, rc, cfg.CheckTimeout, taskID, database.TriggerSchedule)
			if err != nil {
				slog.Error("scheduled task failed", "id", taskID, "err", err)
				return nil
			}
			return &run.ID
		},
		func(checkID int64) *int64 {
			check, err := services.RunCheck(db, rc, checkID, database.TriggerSchedule)
			if err != nil {
				slog.Error("scheduled check failed", "id", checkID, "err", err)
				return nil
			}
			return &check.ID
		})
	if err != nil {
		return err
	}
	a.Sched = sched

	// 10. Leader elector + wiring (HA off when NODE_ID/CLUSTER unset? no —
	// elector is always built; single node just always wins the lease).
	elector := cluster.New(db, cfg.NodeID, cfg.ClusterName,
		time.Duration(cfg.HeartbeatInterval)*time.Second,
		time.Duration(cfg.LeaseDuration)*time.Second)
	elector.OnAcquired = func() { sched.SetLeader(true) }
	elector.OnLost = func() { sched.SetLeader(false) }
	a.Elector = elector
	sched.WithClusterHooks(elector.HeartbeatTick, elector.ElectTick)

	if err := sched.Start(); err != nil {
		return err
	}
	if err := elector.Join(); err != nil {
		slog.Error("elector join failed; HA disabled this run", "err", err)
		// Single-node behavior: act as leader.
		sched.SetLeader(true)
		a.Elector = nil
	} else {
		elector.ElectTick() // synchronous first election → truthful /healthz
	}

	// 11. Bootstrap admin.
	auth.BootstrapAdmin(db, cfg)

	// 12. HTTP server.
	static := DiskStaticFS(cfg.StaticDir) // dev override; embed used when nil
	if static == nil {
		static = rclone_sync.EmbeddedStatic() // release embed; nil with placeholder-only dist
	}
	deps := &Deps{
		DB: db, Cfg: cfg, RC: rc, RCProxy: proxy, Sched: sched, Static: static,
		Elector: func() ElectorInfo {
			if a.Elector == nil {
				return nil
			}
			return a.Elector
		},
	}
	a.Deps = deps
	router := NewRouter(deps)

	addr := net.JoinHostPort(cfg.APIHost, strconv.Itoa(cfg.APIPort))
	a.srv = &http.Server{Addr: addr, Handler: router}
	slog.Info("rclone-sync serving", "addr", addr,
		"leader", a.Elector != nil && a.Elector.IsLeader(), "node", cfg.NodeID)
	if err := a.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown tears everything down in reverse startup order.
func (a *App) Shutdown(ctx context.Context) {
	if a.srv != nil {
		if err := a.srv.Shutdown(ctx); err != nil {
			slog.Error("http shutdown", "err", err)
		}
	}
	if a.Elector != nil {
		a.Elector.Stop()
	}
	if a.Sched != nil {
		_ = a.Sched.Shutdown()
	}
	if a.RC != nil {
		a.RC.Close()
	}
	if a.Manager != nil {
		a.Manager.Stop()
	}
	if a.Cfg.PIDFile != "" {
		_ = os.Remove(a.Cfg.PIDFile)
	}
	slog.Info("rclone-sync stopped")
}
