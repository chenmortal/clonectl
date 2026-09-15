package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
)

// ErrorDetail renders an error for run.error: APIError messages get the
// rclone body error / raw body appended (UI shows this text).
func ErrorDetail(err error) string {
	var apiErr *rclone.APIError
	if errors.As(err, &apiErr) {
		if detail, ok := apiErr.BodyError(); ok {
			return fmt.Sprintf("%v\nrclone: %s", apiErr, detail)
		}
		if apiErr.Body != nil {
			if b, mErr := json.Marshal(apiErr.Body); mErr == nil {
				return fmt.Sprintf("%v\n%s", apiErr, string(b))
			}
		}
	}
	return err.Error()
}

// JoinDSPath composes "{ds_path}/{task_subpath}" with sane slashes:
// trailing / stripped from the base, leading / stripped from the subpath,
// root-only base degenerates to just the subpath.
func JoinDSPath(dsPath, taskSubpath string) string {
	base := strings.TrimRight(dsPath, "/")
	sub := strings.TrimLeft(taskSubpath, "/")
	if base == "" || base == "/" {
		return sub
	}
	if sub == "" {
		return base
	}
	return base + "/" + sub
}

// ResolveRefs pushes the needed remotes and returns (srcFs, dstFs).
func ResolveRefs(db *gorm.DB, client *rclone.Client, task *database.SyncTask) (string, string, error) {
	src, err := resolveSide(db, client, task.SrcDataSourceID, task.SrcPath)
	if err != nil {
		return "", "", err
	}
	dst, err := resolveSide(db, client, task.DstDataSourceID, task.DstPath)
	if err != nil {
		return "", "", err
	}
	return src, dst, nil
}

// resolveSide pushes the ds-N remote and returns the fs spec
// "ds-N:{joined path}" where the path is SidePath(storage, ds.Path) —
// local prefixes baked in — joined with the task subpath.
func resolveSide(db *gorm.DB, client *rclone.Client, dsID int64, path string) (string, error) {
	var ds database.DataSource
	if err := db.First(&ds, dsID).Error; err != nil {
		return "", fmt.Errorf("data source %d not found", dsID)
	}
	var src database.StorageSource
	if err := db.First(&src, ds.StorageSourceID).Error; err != nil {
		return "", fmt.Errorf("storage source %d not found: %w", ds.StorageSourceID, err)
	}
	if err := EnsureDataSourceRemote(db, client, &ds); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s", DSRemoteName(&ds), JoinDSPath(SidePath(&src, ds.Path), path)), nil
}

// RunTask executes one sync task: concurrency guard → run row (pending) →
// remote push → optional blocking pre-check → async sync submission.
// Returns the final run row (failed/running/skipped).
func RunTask(db *gorm.DB, client *rclone.Client, cfgTimeout int, taskID int64, trigger string) (*database.SyncRun, error) {
	var task database.SyncTask
	if err := db.First(&task, taskID).Error; err != nil {
		return nil, fmt.Errorf("task %d not found", taskID)
	}

	// Concurrency guard: pending|running run exists → skipped.
	var active database.SyncRun
	err := db.Where("task_id = ? AND status IN ?", taskID,
		[]string{database.RunPending, database.RunRunning}).First(&active).Error
	if err == nil {
		now := database.NowUTC()
		run := &database.SyncRun{
			TaskID: taskID, Status: database.RunSkipped, Trigger: trigger,
			StartedAt: &now, FinishedAt: &now,
			Error: strPtr("previous run still running"),
		}
		if err := db.Create(run).Error; err != nil {
			return nil, err
		}
		return run, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	run := &database.SyncRun{TaskID: taskID, Status: database.RunPending, Trigger: trigger}
	if err := db.Create(run).Error; err != nil {
		return nil, err
	}

	srcFs, dstFs, err := ResolveRefs(db, client, &task)
	if err == nil && task.PreCheckTaskID != nil {
		var proceed bool
		proceed, err = runPreCheck(db, client, cfgTimeout, &task, run, *task.PreCheckTaskID, trigger)
		if err == nil && !proceed {
			// Run finalized by the pre-check (skipped): stop here.
			return run, nil
		}
	}
	if err == nil {
		var jobID int64
		jobID, err = client.StartSync(srcFs, dstFs, task.Mode, task.RcloneOptions)
		if err == nil {
			now := database.NowUTC()
			run.JobID = &jobID
			run.Status = database.RunRunning
			run.StartedAt = &now
			if err := db.Save(run).Error; err != nil {
				return nil, err
			}
			return run, nil
		}
	}

	// Start failure → failed + alert.
	now := database.NowUTC()
	run.Status = database.RunFailed
	run.StartedAt = &now
	run.FinishedAt = &now
	detail := ErrorDetail(err)
	run.Error = &detail
	if err := db.Save(run).Error; err != nil {
		return nil, err
	}
	slog.Error("run failed to start", "run", run.ID, "err", err)
	NotifySyncRunFailure(db, run)
	return run, nil
}

// runPreCheck blocks the trigger until the pre-check decides.
// Returns (proceed, err):
//   - consistent      → run finalized as skipped(pass msg),  proceed=false
//   - differences     → run untouched,                        proceed=true
//   - error / timeout → run finalized as skipped(blocked msg), proceed=false
func runPreCheck(db *gorm.DB, client *rclone.Client, cfgTimeout int, task *database.SyncTask,
	run *database.SyncRun, preCheckTaskID int64, trigger string) (bool, error) {
	started, err := RunCheck(db, client, preCheckTaskID, trigger)
	if err != nil {
		return false, err
	}
	preCheck, err := WaitForCheck(db, client, started.ID, cfgTimeout, 5*time.Second)
	if err != nil {
		return false, err
	}
	if preCheck != nil && preCheck.Status == database.RunSuccess {
		now := database.NowUTC()
		run.Status = database.RunSkipped
		run.StartedAt = &now
		run.FinishedAt = &now
		msg := fmt.Sprintf("同步前一致性检查通过（check #%d）：两端一致，跳过同步", preCheck.ID)
		run.Error = &msg
		return false, db.Save(run).Error
	}
	// Differences found → proceed with the sync.
	if preCheck != nil && preCheck.Result != nil {
		if v, ok := (*preCheck.Result)["success"]; ok {
			if b, isBool := v.(bool); isBool && !b {
				slog.Info("run proceeding: pre-sync check found differences",
					"run", run.ID, "check", preCheck.ID)
				return true, nil
			}
		}
	}
	// Error / timeout → block.
	now := database.NowUTC()
	run.Status = database.RunSkipped
	run.StartedAt = &now
	run.FinishedAt = &now
	errText := "unknown"
	if preCheck != nil && preCheck.Error != nil {
		errText = *preCheck.Error
	}
	checkRef := int64(0)
	if preCheck != nil {
		checkRef = preCheck.ID
	}
	msg := fmt.Sprintf("同步前一致性检查出错（check #%d）：%s，已阻止同步", checkRef, errText)
	run.Error = &msg
	slog.Warn("run blocked by pre-sync check error", "run", run.ID)
	return false, db.Save(run).Error
}

// PollRun polls one running run and updates it when the rclone job finished.
func PollRun(db *gorm.DB, client *rclone.Client, runID int64) (*database.SyncRun, error) {
	run := &database.SyncRun{}
	if err := db.First(run, runID).Error; err != nil {
		return nil, err
	}
	if run.Status != database.RunRunning || run.JobID == nil {
		return run, nil
	}

	status, err := client.JobStatus(*run.JobID)
	if err != nil {
		now := database.NowUTC()
		run.Status = database.RunFailed
		run.FinishedAt = &now
		detail := ErrorDetail(err)
		run.Error = &detail
		if err := db.Save(run).Error; err != nil {
			return nil, err
		}
		NotifySyncRunFailure(db, run)
		return run, nil
	}

	if finished, _ := status["finished"].(bool); !finished {
		return run, nil
	}

	stats := map[string]any{}
	if s, err := client.JobStats(*run.JobID); err == nil {
		stats = s
	}

	now := database.NowUTC()
	run.FinishedAt = &now
	failed := false
	if v, ok := status["success"].(bool); ok {
		failed = !v
	} else {
		failed = true
	}
	if failed {
		run.Status = database.RunFailed
		msg, ok := status["error"].(string)
		if !ok || msg == "" {
			msg = "unknown rclone error"
		}
		run.Error = &msg
	} else {
		run.Status = database.RunSuccess
	}

	collected := database.JSONObject{}
	for _, key := range []string{"bytes", "checks", "transfers", "errors",
		"elapsedTime", "totalBytes", "totalTransfers"} {
		if v, ok := stats[key]; ok && v != nil {
			collected[key] = v
		}
	}
	if failed {
		collected["jobStatus"] = status
		if len(stats) > 0 {
			collected["jobStats"] = stats
		}
	}
	run.Stats = &collected
	if err := db.Save(run).Error; err != nil {
		return nil, err
	}

	if failed {
		NotifySyncRunFailure(db, run)
	} else {
		NotifySyncRunResolved(db, run)
	}
	return run, nil
}

// PollRunningRuns advances every running sync run (scheduler poll job body).
func PollRunningRuns(db *gorm.DB, client *rclone.Client) {
	var runs []database.SyncRun
	if err := db.Where("status = ?", database.RunRunning).Find(&runs).Error; err != nil {
		slog.Error("poll_running_runs: list failed", "err", err)
		return
	}
	for _, r := range runs {
		if _, err := PollRun(db, client, r.ID); err != nil {
			slog.Error("poll_run failed", "run", r.ID, "err", err)
		}
	}
}

func strPtr(s string) *string { return &s }
