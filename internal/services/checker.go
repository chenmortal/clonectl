package services

import (
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"clonectl/internal/database"
	"clonectl/internal/rclone"
)

// ResultKeys picked from the rclone check output into check_runs.result.
var ResultKeys = []string{
	"success", "status", "hashType", "combined",
	"missingOnSrc", "missingOnDst", "match", "differ", "error",
}

// RunCheck executes one consistency check (no concurrency guard, no
// pre-check — parity with Python).
func RunCheck(db *gorm.DB, client *rclone.Client, checkTaskID int64, trigger string) (*database.CheckRun, error) {
	var task database.CheckTask
	if err := db.First(&task, checkTaskID).Error; err != nil {
		return nil, fmt.Errorf("check task %d not found", checkTaskID)
	}

	check := &database.CheckRun{TaskID: task.ID, Status: database.RunPending, Trigger: trigger}
	if err := db.Create(check).Error; err != nil {
		return nil, err
	}

	src, err := resolveSide(db, client, task.SrcDataSourceID, task.SrcPath)
	var jobID int64
	if err == nil {
		var dst string
		dst, err = resolveSide(db, client, task.DstDataSourceID, task.DstPath)
		if err == nil {
			jobID, err = client.StartCheck(src, dst, task.CheckOptions)
		}
	}
	if err == nil {
		now := database.NowUTC()
		check.JobID = &jobID
		check.Status = database.RunRunning
		check.StartedAt = &now
		if err := db.Save(check).Error; err != nil {
			return nil, err
		}
		return check, nil
	}

	// Submission failure → failed + alert.
	now := database.NowUTC()
	check.Status = database.RunFailed
	check.StartedAt = &now
	check.FinishedAt = &now
	detail := ErrorDetail(err)
	check.Error = &detail
	if err := db.Save(check).Error; err != nil {
		return nil, err
	}
	slog.Error("check failed to start", "check", check.ID, "err", err)
	NotifyCheckRunFailure(db, check)
	return check, nil
}

// PollCheck advances one running check when the rclone job finished.
func PollCheck(db *gorm.DB, client *rclone.Client, checkID int64) (*database.CheckRun, error) {
	check := &database.CheckRun{}
	if err := db.First(check, checkID).Error; err != nil {
		return nil, err
	}
	if check.Status != database.RunRunning || check.JobID == nil {
		return check, nil
	}

	status, err := client.JobStatus(*check.JobID)
	if err != nil {
		now := database.NowUTC()
		check.Status = database.RunFailed
		check.FinishedAt = &now
		detail := ErrorDetail(err)
		check.Error = &detail
		if err := db.Save(check).Error; err != nil {
			return nil, err
		}
		NotifyCheckRunFailure(db, check)
		return check, nil
	}

	if finished, _ := status["finished"].(bool); !finished {
		return check, nil
	}

	now := database.NowUTC()
	check.FinishedAt = &now
	output := map[string]any{}
	if raw, ok := status["output"].(map[string]any); ok {
		output = raw
	}
	result := database.JSONObject{}
	for _, key := range ResultKeys {
		if v, ok := output[key]; ok {
			result[key] = v
		}
	}
	check.Result = &result

	bodySuccess, _ := status["success"].(bool)
	outSuccess := false
	if v, ok := output["success"].(bool); ok {
		outSuccess = v
	}
	switch {
	case !bodySuccess:
		check.Status = database.RunFailed
		msg, ok := status["error"].(string)
		if !ok || msg == "" {
			msg = "rclone check job failed"
		}
		check.Error = &msg
	case outSuccess:
		check.Status = database.RunSuccess
		check.Error = nil
	default:
		check.Status = database.RunFailed
		msg := fmt.Sprintf("一致性检查发现差异：%d 个文件不一致，源端缺失 %d，目标端缺失 %d，错误 %d",
			countList(output["differ"]), countList(output["missingOnSrc"]),
			countList(output["missingOnDst"]), countList(output["error"]))
		check.Error = &msg
	}
	if err := db.Save(check).Error; err != nil {
		return nil, err
	}

	if check.Status == database.RunFailed {
		NotifyCheckRunFailure(db, check)
	} else {
		NotifyCheckRunResolved(db, check)
	}
	return check, nil
}

func countList(v any) int {
	if list, ok := v.([]any); ok {
		return len(list)
	}
	return 0
}

// PollRunningChecks advances every running check run.
func PollRunningChecks(db *gorm.DB, client *rclone.Client) {
	var checks []database.CheckRun
	if err := db.Where("status = ?", database.RunRunning).Find(&checks).Error; err != nil {
		slog.Error("poll_running_checks: list failed", "err", err)
		return
	}
	for _, c := range checks {
		if _, err := PollCheck(db, client, c.ID); err != nil {
			slog.Error("poll_check failed", "check", c.ID, "err", err)
		}
	}
}

// WaitForCheck polls until the check leaves running or the deadline passes.
// Timeout marks the check failed ("check timed out after Ns") + alert.
func WaitForCheck(db *gorm.DB, client *rclone.Client, checkID int64,
	timeoutSeconds int, interval time.Duration) (*database.CheckRun, error) {
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		check, err := PollCheck(db, client, checkID)
		if err != nil {
			return nil, err
		}
		if check == nil || check.Status != database.RunRunning {
			return check, nil
		}
		time.Sleep(interval)
	}
	check := &database.CheckRun{}
	if err := db.First(check, checkID).Error; err != nil {
		return nil, err
	}
	if check.Status == database.RunRunning {
		now := database.NowUTC()
		check.Status = database.RunFailed
		check.FinishedAt = &now
		msg := fmt.Sprintf("check timed out after %ds", timeoutSeconds)
		check.Error = &msg
		if err := db.Save(check).Error; err != nil {
			return nil, err
		}
		NotifyCheckRunFailure(db, check)
	}
	return check, nil
}
