package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"

	"rclone_sync/internal/database"
)

// Alertmanager v4 webhook integration (port of app/services/alerts.py).
// Delivery failures are logged and swallowed — the poller must keep going.

var amClient = &http.Client{Timeout: 5 * time.Second}

// ToISOZ formats t as RFC3339 with Z; nil → AlertManager's still-firing sentinel.
func ToISOZ(t *time.Time) string {
	if t == nil {
		return "0001-01-01T00:00:00Z"
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// GetAlertmanagerURL reads the system setting ("" = disabled).
func GetAlertmanagerURL(db *gorm.DB) string {
	var row database.SystemSetting
	if err := db.First(&row, "key = ?", database.SettingAlertmanagerURL).Error; err != nil {
		return ""
	}
	return row.Value
}

// BuildAlertmanagerPayload assembles the v4 webhook body.
func BuildAlertmanagerPayload(taskID int64, taskName string, runID int64, errorText string,
	startedAt *time.Time, status string, labels map[string]string, isSync bool, endsAt *time.Time,
) map[string]any {
	kind := "sync"
	if !isSync {
		kind = "check"
	}
	alertname := labels["alertname"]
	if alertname == "" {
		alertname = "SyncTaskFailed"
	}
	commonLabels := map[string]string{
		"alertname": alertname,
		"severity":  "critical",
		"service":   "rclone-sync",
		"task_id":   fmt.Sprintf("%d", taskID),
		"task_name": taskName,
		"task_kind": kind,
		"run_id":    fmt.Sprintf("%d", runID),
	}
	for k, v := range labels {
		commonLabels[k] = v
	}
	summaryKind := "rclone sync"
	if !isSync {
		summaryKind = "rclone check"
	}
	annotations := map[string]string{
		"summary":     fmt.Sprintf("%s failed: %s (run #%d)", summaryKind, taskName, runID),
		"description": errorText,
	}
	if annotations["description"] == "" {
		annotations["description"] = "unknown rclone error"
	}
	if isSync {
		annotations["run_url"] = fmt.Sprintf("/runs/%d", runID)
	} else {
		annotations["run_url"] = fmt.Sprintf("/checks/%d", runID)
	}
	endsAtISO := "0001-01-01T00:00:00Z"
	if status == "resolved" {
		now := time.Now().UTC()
		if endsAt != nil {
			now = *endsAt
		}
		endsAtISO = ToISOZ(&now)
	}
	return map[string]any{
		"version":           "4",
		"groupKey":          fmt.Sprintf("{run.%s}.%d", kind, taskID),
		"status":            status,
		"receiver":          "rclone-sync",
		"groupLabels":       map[string]any{"alertname": alertname, "task_id": fmt.Sprintf("%d", taskID)},
		"commonLabels":      commonLabels,
		"commonAnnotations": annotations,
		"externalURL":       "",
		"alerts": []any{map[string]any{
			"status":       status,
			"labels":       commonLabels,
			"annotations":  annotations,
			"startsAt":     ToISOZ(startedAt),
			"endsAt":       endsAtISO,
			"generatorURL": "",
		}},
	}
}

// PostToAlertmanager delivers a payload. Returns (sent, statusCode, error).
func PostToAlertmanager(url string, payload map[string]any) (bool, int, string) {
	if url == "" {
		return false, 0, "no URL"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, 0, err.Error()
	}
	resp, err := amClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Warn("alertmanager POST failed", "url", url, "err", err)
		return false, 0, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf := make([]byte, 500)
		n, _ := resp.Body.Read(buf)
		slog.Warn("alertmanager returned error", "url", url, "status", resp.StatusCode, "body", string(buf[:n]))
		return false, resp.StatusCode, string(buf[:n])
	}
	return true, resp.StatusCode, ""
}

// NotifySyncRunFailure sends a firing webhook for a failed sync run
// (notified_at dedup; non-failed rows never alert).
func NotifySyncRunFailure(db *gorm.DB, run *database.SyncRun) {
	if run.Status != database.RunFailed || run.NotifiedAt != nil {
		return
	}
	url := GetAlertmanagerURL(db)
	if url == "" {
		return
	}
	var task database.SyncTask
	if err := db.First(&task, run.TaskID).Error; err != nil {
		return
	}
	payload := BuildAlertmanagerPayload(task.ID, task.Name, run.ID, deref(run.Error), run.StartedAt,
		"firing", map[string]string{"alertname": "SyncTaskFailed"}, true, nil)
	if sent, _, _ := PostToAlertmanager(url, payload); sent {
		now := database.NowUTC()
		run.NotifiedAt = &now
		db.Save(run)
		slog.Info("alertmanager notified: firing", "run", run.ID, "task", task.ID)
	}
}

// NotifyCheckRunFailure is the check-task variant.
func NotifyCheckRunFailure(db *gorm.DB, run *database.CheckRun) {
	if run.Status != database.RunFailed || run.NotifiedAt != nil {
		return
	}
	url := GetAlertmanagerURL(db)
	if url == "" {
		return
	}
	var task database.CheckTask
	if err := db.First(&task, run.TaskID).Error; err != nil {
		return
	}
	payload := BuildAlertmanagerPayload(task.ID, task.Name, run.ID, deref(run.Error), run.StartedAt,
		"firing", map[string]string{"alertname": "CheckTaskFailed"}, false, nil)
	if sent, _, _ := PostToAlertmanager(url, payload); sent {
		now := database.NowUTC()
		run.NotifiedAt = &now
		db.Save(run)
		slog.Info("alertmanager notified: firing", "check", run.ID, "task", task.ID)
	}
}

// NotifySyncRunResolved closes a prior firing when a fresh success lands for
// the same task; clears the old row's notified_at regardless of delivery.
func NotifySyncRunResolved(db *gorm.DB, run *database.SyncRun) {
	if run.Status != database.RunSuccess {
		return
	}
	url := GetAlertmanagerURL(db)
	if url == "" {
		return
	}
	var task database.SyncTask
	if err := db.First(&task, run.TaskID).Error; err != nil {
		return
	}
	prev := &database.SyncRun{}
	if err := db.Where("task_id = ? AND status = ? AND notified_at IS NOT NULL", task.ID, database.RunFailed).
		Order("id DESC").First(prev).Error; err != nil {
		return // nothing to resolve
	}
	payload := BuildAlertmanagerPayload(task.ID, task.Name, run.ID, "", prev.StartedAt,
		"resolved", map[string]string{"alertname": "SyncTaskFailed"}, true, nowPtr())
	sent, _, _ := PostToAlertmanager(url, payload)
	prev.NotifiedAt = nil
	if err := db.Save(prev).Error; err != nil {
		slog.Error("alertmanager: clear notified_at failed", "err", err)
	}
	if sent {
		slog.Info("alertmanager notified: resolved", "run", run.ID, "task", task.ID, "closes", prev.ID)
	}
}

// NotifyCheckRunResolved is the check-task variant.
func NotifyCheckRunResolved(db *gorm.DB, run *database.CheckRun) {
	if run.Status != database.RunSuccess {
		return
	}
	url := GetAlertmanagerURL(db)
	if url == "" {
		return
	}
	var task database.CheckTask
	if err := db.First(&task, run.TaskID).Error; err != nil {
		return
	}
	prev := &database.CheckRun{}
	if err := db.Where("task_id = ? AND status = ? AND notified_at IS NOT NULL", task.ID, database.RunFailed).
		Order("id DESC").First(prev).Error; err != nil {
		return
	}
	payload := BuildAlertmanagerPayload(task.ID, task.Name, run.ID, "", prev.StartedAt,
		"resolved", map[string]string{"alertname": "CheckTaskFailed"}, false, nowPtr())
	sent, _, _ := PostToAlertmanager(url, payload)
	prev.NotifiedAt = nil
	if err := db.Save(prev).Error; err != nil {
		slog.Error("alertmanager: clear notified_at failed", "err", err)
	}
	if sent {
		slog.Info("alertmanager notified: resolved", "check", run.ID, "task", task.ID, "closes", prev.ID)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nowPtr() *time.Time {
	t := time.Now().UTC()
	return &t
}
