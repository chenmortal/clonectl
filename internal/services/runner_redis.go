// Package services — runner_redis.go: dispatch Sync/Check tasks to
// the redis-shake-agent via the agent.Driver abstraction.
//
// Two top-level entry points:
//
//   runRedisShakeTask(db, ctx, task, trigger) — sync mode.
//   runRedisFullCheckTask(db, ctx, task, trigger) — verify mode.
//
// Both share a common helper (resolveAgentEndpoint) which decides
// which agent handles the task based on DataSource labels and the
// configured Locator. The rest of each function reads the source /
// target DataSource rows, builds an agent.Spec, calls Driver.Submit,
// and stores the agent task_id on the SyncRun/CheckRun row.
//
// Error handling:
//   - Driver returns ErrAgentUnreachable → SyncRun.Status = failed,
//     error message = "agent_unreachable: ...". We do NOT retry;
//     the scheduler will pick up the next cron tick.
//   - Driver returns ErrUnsupportedMode → SyncRun.Status = failed,
//     but the alertmanager rule treats this as a non-actionable
//     config error.
//   - Driver returns ErrInvalidSpec → same as above.
//
// Polling: RunTaskWith returns once Submit has succeeded; the
// existing poll-running-runs goroutine in scheduler/service.go will
// pick the new run up on its next tick (default 5s) and call
// Driver.Status to refresh progress counters. The agent task_id is
// stored on SyncRun.AgentTaskID (a new column) since JobID stays as
// the rclone-side int64.
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"clonectl/internal/agent"
	"clonectl/internal/database"
)

// resolveAgentEndpoint picks the right agent for the task based on
// the source DataSource labels. Returns the dial address and the
// Driver to use. The driver is keyed by the task's tool_kind (the
// caller knows which kind it asked for).
//
// When ctx.Locator is nil, we build a tiny inline locator with an
// empty default endpoint — that surfaces a clean error to the run
// row instead of silently picking an unreachable host.
func resolveAgentEndpoint(ctx RunnerContext, srcDS *database.DataSource, srcSS *database.StorageSource, kind agent.ToolKind) (string, agent.Driver, error) {
	if ctx.Agents == nil {
		return "", nil, fmt.Errorf("no agent registry configured (control-plane-only install?)")
	}
	drv := ctx.Agents.Get(kind)
	if drv == nil {
		return "", nil, fmt.Errorf("no driver registered for tool_kind=%s", kind)
	}

	labels := extractRoutingLabels(srcSS)
	locator := ctx.Locator
	if locator == nil {
		locator = agent.NewLocator("")
	}
	endpoint, err := locator.Resolve(labels)
	if err != nil {
		return "", nil, err
	}
	return string(endpoint), drv, nil
}

// extractRoutingLabels flattens the StorageSource.Extra map into a
// string map the Locator understands. v1 only reads agent_group /
// region; future keys can be added here.
func extractRoutingLabels(src *database.StorageSource) map[string]string {
	out := map[string]string{}
	if v, ok := src.Extra["agent_group"].(string); ok && v != "" {
		out["agent_group"] = v
	}
	if v, ok := src.Extra["region"].(string); ok && v != "" {
		out["region"] = v
	}
	return out
}

// buildAgentSpec renders the source / target DataSource pair into
// the tool-agnostic agent.Spec. Source/target endpoints come from
// each DataSource's StorageSource.Extra (mode + addresses +
// master_name for sentinel). Passwords (per-DSN) flow through to
// Secrets.
func buildAgentSpec(db *gorm.DB, srcID, dstID int64) (agent.Spec, agent.Secrets, error) {
	var spec agent.Spec
	var secrets agent.Secrets

	srcDS, srcSS, err := loadDataSourceWithStorage(db, srcID)
	if err != nil {
		return spec, secrets, fmt.Errorf("source data source: %w", err)
	}
	dstDS, dstSS, err := loadDataSourceWithStorage(db, dstID)
	if err != nil {
		return spec, secrets, fmt.Errorf("target data source: %w", err)
	}

	spec.Source = endpointFromDataSource(srcDS, srcSS)
	spec.Target = endpointFromDataSource(dstDS, dstSS)

	if srcDS.Password != nil {
		secrets.SourcePassword = *srcDS.Password
	}
	if dstDS.Password != nil {
		secrets.TargetPassword = *dstDS.Password
	}
	return spec, secrets, nil
}

// loadDataSourceWithStorage fetches one DataSource row plus its
// referenced StorageSource. Used by both runners to assemble the
// agent Spec.
func loadDataSourceWithStorage(db *gorm.DB, id int64) (*database.DataSource, *database.StorageSource, error) {
	var ds database.DataSource
	if err := db.First(&ds, id).Error; err != nil {
		return nil, nil, err
	}
	var ss database.StorageSource
	if err := db.First(&ss, ds.StorageSourceID).Error; err != nil {
		return &ds, nil, fmt.Errorf("storage source %d: %w", ds.StorageSourceID, err)
	}
	return &ds, &ss, nil
}

// endpointFromDataSource renders a (DataSource, StorageSource) pair
// into the agent.Endpoint shape. The StorageSource.Extra carries
// mode / addresses / master_name; RedisConfigs (per-DSN) carries
// db / key_prefix / tls.
func endpointFromDataSource(ds *database.DataSource, src *database.StorageSource) agent.Endpoint {
	mode, _ := src.Extra["mode"].(string)
	if mode == "" {
		mode = "standalone"
	}
	ep := agent.Endpoint{Mode: mode}
	if arr, ok := src.Extra["addresses"].([]any); ok {
		for _, a := range arr {
			if s, ok := a.(string); ok && s != "" {
				ep.Addresses = append(ep.Addresses, s)
			}
		}
	}
	if mn, ok := src.Extra["master_name"].(string); ok {
		ep.MasterName = mn
	}
	if u, ok := src.Extra["username"].(string); ok {
		ep.Username = u
	}
	// Per-DSN knobs from RedisConfigs.
	if dbIdx, ok := ds.RedisConfigs["db"].(float64); ok {
		ep.DB = int(dbIdx)
	}
	if kp, ok := ds.RedisConfigs["key_prefix"].(string); ok {
		ep.KeyPrefix = kp
	}
	return ep
}

// runRedisShakeTask is the redis-shake dispatcher. It creates the
// run row, resolves the agent, submits, and stores the agent-side
// task_id on the run so the scheduler's poller can refresh progress.
func runRedisShakeTask(db *gorm.DB, ctx RunnerContext, task *database.SyncTask, trigger string) (*database.SyncRun, error) {
	now := database.NowUTC()
	run := &database.SyncRun{
		TaskID:    task.ID,
		Status:    database.RunPending,
		Trigger:   trigger,
		StartedAt: &now,
	}
	if err := db.Create(run).Error; err != nil {
		return nil, err
	}

	srcDS, srcSS, err := loadDataSourceWithStorage(db, task.SrcDataSourceID)
	if err != nil {
		return failSyncRun(db, run, "load source: "+err.Error())
	}
	endpoint, drv, err := resolveAgentEndpoint(ctx, srcDS, srcSS, agent.ToolRedisShake)
	if err != nil {
		return failSyncRun(db, run, err.Error())
	}

	spec, secrets, err := buildAgentSpec(db, task.SrcDataSourceID, task.DstDataSourceID)
	if err != nil {
		return failSyncRun(db, run, err.Error())
	}
	spec.Mode = task.RedisMode
	if spec.Mode == "" {
		spec.Mode = "sync_reader" // safe default
	}

	req := agent.SubmitRequest{
		Tool:    agent.ToolRedisShake,
		TaskID:  fmt.Sprintf("sync-%d", run.ID),
		Mode:    spec.Mode,
		Spec:    spec,
		Secrets: secrets,
	}

	submitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := drv.Submit(submitCtx, endpoint, req)
	if err != nil {
		msg := "agent_submit: " + err.Error()
		if agent.IsUnsupported(err) {
			msg = "unsupported_mode: " + err.Error()
		}
		return failSyncRun(db, run, msg)
	}

	now2 := database.NowUTC()
	run.Status = database.RunRunning
	run.StartedAt = &now2
	run.AgentTaskID = &resp.TaskID
	if err := db.Save(run).Error; err != nil {
		return run, err
	}
	return run, nil
}

// runRedisFullCheckTask is the redis-fullcheck dispatcher.
func runRedisFullCheckTask(db *gorm.DB, ctx RunnerContext, task *database.CheckTask, trigger string) (*database.CheckRun, error) {
	now := database.NowUTC()
	run := &database.CheckRun{
		TaskID:    task.ID,
		Status:    database.RunPending,
		Trigger:   trigger,
		StartedAt: &now,
	}
	if err := db.Create(run).Error; err != nil {
		return nil, err
	}

	srcDS, srcSS, err := loadDataSourceWithStorage(db, task.SrcDataSourceID)
	if err != nil {
		return failCheckRun(db, run, "load source: "+err.Error())
	}
	endpoint, drv, err := resolveAgentEndpoint(ctx, srcDS, srcSS, agent.ToolRedisFullCheck)
	if err != nil {
		return failCheckRun(db, run, err.Error())
	}

	spec, secrets, err := buildAgentSpec(db, task.SrcDataSourceID, task.DstDataSourceID)
	if err != nil {
		return failCheckRun(db, run, err.Error())
	}
	mode := fmt.Sprintf("%d", task.RedisCompareMode)
	if task.RedisCompareMode == 0 {
		mode = "1"
	}
	// Spec.Mode is also required — the driver validates it before
	// forwarding to the agent. Both fields must agree.
	spec.Mode = mode

	req := agent.SubmitRequest{
		Tool:    agent.ToolRedisFullCheck,
		TaskID:  fmt.Sprintf("check-%d", run.ID),
		Mode:    mode,
		Spec:    spec,
		Secrets: secrets,
	}

	submitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := drv.Submit(submitCtx, endpoint, req)
	if err != nil {
		msg := "agent_submit: " + err.Error()
		if agent.IsUnsupported(err) {
			msg = "unsupported_mode: " + err.Error()
		}
		return failCheckRun(db, run, msg)
	}

	now2 := database.NowUTC()
	run.Status = database.RunRunning
	run.StartedAt = &now2
	run.AgentTaskID = &resp.TaskID
	if err := db.Save(run).Error; err != nil {
		return run, err
	}
	return run, nil
}

// failSyncRun / failCheckRun mark a run failed, persist, and return
// the run + error so callers can surface it via the API.
func failSyncRun(db *gorm.DB, run *database.SyncRun, msg string) (*database.SyncRun, error) {
	now := database.NowUTC()
	run.Status = database.RunFailed
	run.FinishedAt = &now
	run.Error = strPtr(msg)
	if err := db.Save(run).Error; err != nil {
		return run, err
	}
	return run, errors.New(msg)
}

func failCheckRun(db *gorm.DB, run *database.CheckRun, msg string) (*database.CheckRun, error) {
	now := database.NowUTC()
	run.Status = database.RunFailed
	run.FinishedAt = &now
	run.Error = strPtr(msg)
	if err := db.Save(run).Error; err != nil {
		return run, err
	}
	return run, errors.New(msg)
}
