// Package server — request handlers.
//
// Each handler returns the standard envelope:
//
//	{"ok": true,  "data": {...}}
//	{"ok": false, "error": {"code": "...", "message": "..."}}
//
// Errors carry stable codes (auth_failed, invalid_spec,
// unsupported_mode, agent_unreachable, ...) so clonectl's driver layer
// can map them to TaskState values without string matching.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/chenmortal/redis-shake-agent/internal/runner"
	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// Server holds the agent's HTTP-facing dependencies.
type Server struct {
	Cfg     runner.AgentConfig
	Store   *store.TaskStore
	Iso     *runner.Isolation
}

// Register installs every handler on r.
func (s *Server) Register(r *gin.Engine) {
	v1 := r.Group("/v1")
	v1.GET("/ping", s.handlePing)
	v1.GET("/noop", s.handleNoop)

	v1.POST("/tasks/submit", s.handleSubmit)
	v1.GET("/tasks/list", s.handleList)
	v1.GET("/tasks/status", s.handleStatus)
	v1.DELETE("/tasks/stop", s.handleStop)
	v1.GET("/tasks/logs", s.handleLogs)
	v1.GET("/tasks/metrics", s.handleMetrics)

	v1.GET("/version", s.handleVersion)
	v1.GET("/binary-info", s.handleBinaryInfo)
}

// --- envelope helpers ---

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": data})
}

func fail(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"ok": false, "error": gin.H{"code": code, "message": msg}})
}

// --- /v1/ping ---

func (s *Server) handlePing(c *gin.Context) {
	ok(c, gin.H{"now": time.Now().UTC()})
}

func (s *Server) handleNoop(c *gin.Context) {
	ok(c, gin.H{"noop": true})
}

// --- /v1/version ---

func (s *Server) handleVersion(c *gin.Context) {
	ok(c, gin.H{
		"agent": gin.H{"version": agentVersion, "commit": agentCommit},
		"upstream": gin.H{
			"redis-shake":     s.Cfg.UpstreamVersionPinned["redis-shake"],
			"redis-fullcheck": s.Cfg.UpstreamVersionPinned["redis-fullcheck"],
		},
		"binary_paths": gin.H{
			"redis-shake":     s.Cfg.Shake.Binary,
			"redis-fullcheck": s.Cfg.FullCheck.Binary,
		},
	})
}

// --- /v1/binary-info ---

func (s *Server) handleBinaryInfo(c *gin.Context) {
	ok(c, gin.H{
		"redis-shake": gin.H{
			"binary":    s.Cfg.Shake.Binary,
			"available": binaryExists(s.Cfg.Shake.Binary),
			"modes":     []string{"sync_reader", "rdb_reader", "scan_reader"},
		},
		"redis-fullcheck": gin.H{
			"binary":    s.Cfg.FullCheck.Binary,
			"available": binaryExists(s.Cfg.FullCheck.Binary),
			"compare_modes": gin.H{
				"1": "full",
				"2": "length",
				"3": "existence",
				"4": "adaptive",
			},
		},
	})
}

func binaryExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// --- /v1/tasks/submit ---

type submitSecrets struct {
	SourcePassword string `json:"source_password"`
	TargetPassword string `json:"target_password"`
}

type submitEnvelope struct {
	Tool         string          `json:"tool"`
	TaskID       string          `json:"task_id"`
	Mode         string          `json:"mode"`
	Config       json.RawMessage `json:"config"`
	Secrets      submitSecrets   `json:"secrets"`
}

func (s *Server) handleSubmit(c *gin.Context) {
	var req submitEnvelope
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_spec", "decode body: "+err.Error())
		return
	}
	ctx := c.Request.Context()
	switch req.Tool {
	case "redis-shake":
		t, err := runner.SubmitShake(ctx, s.Cfg.Shake, s.Iso, s.Store,
			runner.ShakeRequest{
				Tool: req.Tool, TaskID: req.TaskID, Mode: req.Mode, Config: req.Config,
			},
			runner.ShakeSecrets{
				SourcePassword: req.Secrets.SourcePassword,
				TargetPassword: req.Secrets.TargetPassword,
			},
		)
		if err != nil {
			respondSubmitErr(c, err)
			return
		}
		ok(c, gin.H{
			"task_id":    t.TaskID,
			"pid":        t.Process.PID(),
			"started_at_unix_ms": t.StartedAt.UnixMilli(),
			"state":      "running",
		})
	case "redis-fullcheck":
		t, err := runner.SubmitFullCheck(ctx, s.Cfg.FullCheck, s.Iso, s.Store,
			runner.FullCheckRequest{
				Tool: req.Tool, TaskID: req.TaskID, Mode: req.Mode, Config: req.Config,
			},
			runner.FullCheckSecrets{
				SourcePassword: req.Secrets.SourcePassword,
				TargetPassword: req.Secrets.TargetPassword,
			},
		)
		if err != nil {
			respondSubmitErr(c, err)
			return
		}
		ok(c, gin.H{
			"task_id":    t.TaskID,
			"pid":        t.Process.PID(),
			"started_at_unix_ms": t.StartedAt.UnixMilli(),
			"state":      "running",
		})
	default:
		fail(c, http.StatusBadRequest, "unsupported_mode", "unknown tool "+req.Tool)
	}
}

// respondSubmitErr maps runner errors to HTTP + envelope codes.
// The agent intentionally returns 400 (not 500) on validation errors
// because clonectl's driver treats 4xx as a per-task problem, not
// an agent outage.
func respondSubmitErr(c *gin.Context, err error) {
	var de *runner.DriverError
	if errors.As(err, &de) {
		status := http.StatusBadRequest
		switch de.Code {
		case "unsupported_mode":
			status = http.StatusBadRequest
		case "agent_unreachable":
			status = http.StatusBadGateway
		case "auth_failed":
			status = http.StatusUnauthorized
		case "task_not_found":
			status = http.StatusNotFound
		case "upstream_crash":
			status = http.StatusBadGateway
		}
		fail(c, status, de.Code, de.Message)
		return
	}
	fail(c, http.StatusInternalServerError, "upstream_crash", err.Error())
}

// --- /v1/tasks/list ---

func (s *Server) handleList(c *gin.Context) {
	filter := store.Tool(c.Query("tool"))
	rows := s.Store.List(filter)
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, gin.H{
			"task_id":    r.TaskID,
			"tool":       r.Tool,
			"mode":       r.Mode,
			"state":      r.State,
			"started_at": r.StartedAt,
		})
	}
	ok(c, gin.H{"tasks": out})
}

// --- /v1/tasks/status ---

func (s *Server) handleStatus(c *gin.Context) {
	id := c.Query("task_id")
	if id == "" {
		fail(c, http.StatusBadRequest, "invalid_spec", "task_id query required")
		return
	}
	t, err := s.Store.Get(id)
	if err != nil {
		fail(c, http.StatusNotFound, "task_not_found", id)
		return
	}

	state := t.State()
	pid := 0
	exitCode := -1
	startedAt := int64(0)
	endedAt := int64(0)
	if t.Process != nil {
		pid = t.Process.PID()
		exitCode = t.Process.ExitCode()
		startedAt = t.Process.StartedAt().UnixMilli()
		if !t.Process.EndedAt().IsZero() {
			endedAt = t.Process.EndedAt().UnixMilli()
		}
	}

	// Progress is computed via the appropriate scraper.
	var raw json.RawMessage
	switch t.Tool {
	case store.ToolShake:
		res := runner.ScrapeShakeProgress(c.Request.Context(), t, s.shakeMetricsURL(t))
		raw = res.Raw
		_ = res
	case store.ToolFullCheck:
		res := runner.ScrapeFullCheckProgress(t)
		raw = res.Raw
		_ = res
	}

	ok(c, gin.H{
		"task_id":             t.TaskID,
		"tool":                string(t.Tool),
		"mode":                t.Mode,
		"state":               state,
		"pid":                 pid,
		"exit_code":           exitCode,
		"started_at_unix_ms":  startedAt,
		"ended_at_unix_ms":    endedAt,
		"work_dir":            t.WorkDir,
		"result_db":           t.ResultDBPath,
		"raw":                 raw,
	})
}

// shakeMetricsURL builds the URL to scrape when the upstream wrapper
// is configured. Returns "" when the wrapper is not configured.
func (s *Server) shakeMetricsURL(t *store.Task) string {
	// For v1, the agent does not actually fork the wrapper alongside
	// the main process — full support is staged for a follow-up PR.
	// Returning "" forces the ScrapeShakeProgress fallback (stdout
	// regex), which keeps the agent functional even when the wrapper
	// binary is absent.
	return ""
}

// --- /v1/tasks/stop ---

func (s *Server) handleStop(c *gin.Context) {
	id := c.Query("task_id")
	if id == "" {
		fail(c, http.StatusBadRequest, "invalid_spec", "task_id query required")
		return
	}
	t, err := s.Store.Get(id)
	if err != nil {
		fail(c, http.StatusNotFound, "task_not_found", id)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	if err := runner.StopShake(ctx, t); err != nil {
		fail(c, http.StatusBadGateway, "upstream_crash", err.Error())
		return
	}
	ok(c, gin.H{"task_id": id, "state": "stopped"})
}

// --- /v1/tasks/logs ---

func (s *Server) handleLogs(c *gin.Context) {
	id := c.Query("task_id")
	if id == "" {
		fail(c, http.StatusBadRequest, "invalid_spec", "task_id query required")
		return
	}
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit == 0 {
		limit = 64 * 1024
	}
	t, err := s.Store.Get(id)
	if err != nil {
		fail(c, http.StatusNotFound, "task_not_found", id)
		return
	}
	content, nextOffset, eof, err := runner.ReadShakeLog(t, offset, limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, "upstream_crash", err.Error())
		return
	}
	ok(c, gin.H{
		"task_id": id,
		"offset":  nextOffset,
		"content": string(content),
		"eof":     eof,
	})
}

// --- /v1/tasks/metrics ---

func (s *Server) handleMetrics(c *gin.Context) {
	id := c.Query("task_id")
	if id == "" {
		fail(c, http.StatusBadRequest, "invalid_spec", "task_id query required")
		return
	}
	t, err := s.Store.Get(id)
	if err != nil {
		fail(c, http.StatusNotFound, "task_not_found", id)
		return
	}
	var p runner.Progress
	switch t.Tool {
	case store.ToolShake:
		p = runner.ScrapeShakeProgress(c.Request.Context(), t, s.shakeMetricsURL(t)).Progress
	case store.ToolFullCheck:
		p = runner.ScrapeFullCheckProgress(t).Progress
	}
	ok(c, gin.H{"task_id": id, "progress": p})
}

// version vars are overridden at release build time via -ldflags.
var (
	agentVersion = "dev"
	agentCommit  = "none"
)

// avoid unused-import warning when this file is the only consumer
// of these helpers in the package.
var (
	_ = fmt.Sprintf
)
