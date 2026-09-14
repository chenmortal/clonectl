package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"rclone_sync/internal/database"
)

func alertDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}

func TestBuildAlertmanagerPayloadFiring(t *testing.T) {
	started := time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)
	p := BuildAlertmanagerPayload(7, "nightly", 42, "boom", &started,
		"firing", map[string]string{"alertname": "SyncTaskFailed"}, true, nil)

	assert.Equal(t, "4", p["version"])
	assert.Equal(t, "{run.sync}.7", p["groupKey"])
	assert.Equal(t, "firing", p["status"])
	assert.Equal(t, "rclone-sync", p["receiver"])

	alerts := p["alerts"].([]any)
	a := alerts[0].(map[string]any)
	assert.Equal(t, "2026-09-14T03:00:00Z", a["startsAt"])
	assert.Equal(t, "0001-01-01T00:00:00Z", a["endsAt"], "firing sentinel")
	labels := a["labels"].(map[string]string)
	assert.Equal(t, "SyncTaskFailed", labels["alertname"])
	assert.Equal(t, "critical", labels["severity"])
	assert.Equal(t, "7", labels["task_id"])
	assert.Equal(t, "42", labels["run_id"])
	assert.Equal(t, "sync", labels["task_kind"])
	ann := a["annotations"].(map[string]string)
	assert.Equal(t, "/runs/42", ann["run_url"])
	assert.Equal(t, "boom", ann["description"])
}

func TestBuildAlertmanagerPayloadResolvedCheck(t *testing.T) {
	now := time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)
	p := BuildAlertmanagerPayload(3, "check-it", 9, "", nil,
		"resolved", map[string]string{"alertname": "CheckTaskFailed"}, false, &now)

	assert.Equal(t, "{run.check}.3", p["groupKey"])
	alerts := p["alerts"].([]any)
	a := alerts[0].(map[string]any)
	assert.Equal(t, "2026-09-14T04:00:00Z", a["endsAt"])
	assert.Equal(t, "0001-01-01T00:00:00Z", a["startsAt"], "nil started_at → sentinel")
	ann := a["annotations"].(map[string]string)
	assert.Equal(t, "/checks/9", ann["run_url"])
	assert.Equal(t, "unknown rclone error", ann["description"], "empty error → fallback text")
}

func TestPostToAlertmanagerSwallowsErrors(t *testing.T) {
	sent, code, errStr := PostToAlertmanager("", map[string]any{})
	assert.False(t, sent)
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, errStr)

	sent, _, _ = PostToAlertmanager("http://127.0.0.1:1/nope", map[string]any{})
	assert.False(t, sent)
}

func TestNotifyRunFailureAndResolved(t *testing.T) {
	db := alertDB(t)

	var mu sync.Mutex
	var payloads []map[string]any
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		payloads = append(payloads, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer am.Close()
	require.NoError(t, db.Create(&database.SystemSetting{
		Key: database.SettingAlertmanagerURL, Value: am.URL,
	}).Error)

	admin := database.User{Username: "root", PasswordHash: "x", Role: database.RoleAdmin}
	require.NoError(t, db.Create(&admin).Error)
	task := database.SyncTask{Name: "t", SrcDataSourceID: 1, DstDataSourceID: 1,
		SrcPath: "/a", DstPath: "/b", Mode: database.ModeSync, Cron: "0 3 * * *", Enabled: true,
		RcloneOptions: database.JSONObject{}}
	require.NoError(t, db.Create(&task).Error)

	// Failure fires once; dedup on re-entry.
	run := &database.SyncRun{TaskID: task.ID, Status: database.RunFailed,
		Trigger: database.TriggerManual, Error: strPtr("boom")}
	require.NoError(t, db.Create(run).Error)
	NotifySyncRunFailure(db, run)
	NotifySyncRunFailure(db, run)
	require.Len(t, payloads, 1, "notified_at dedups")
	require.NotNil(t, run.NotifiedAt, "notified_at set after successful fire")

	// A fresh success resolves + clears notified_at on the failed row.
	okRun := &database.SyncRun{TaskID: task.ID, Status: database.RunSuccess,
		Trigger: database.TriggerManual}
	require.NoError(t, db.Create(okRun).Error)
	NotifySyncRunResolved(db, okRun)
	require.Len(t, payloads, 2)
	assert.Equal(t, "resolved", payloads[1]["status"])
	// Reload into a FRESH struct: GORM's First does not overwrite a non-nil
	// pointer field with a NULL column value on an already-populated struct.
	var reloaded database.SyncRun
	require.NoError(t, db.First(&reloaded, run.ID).Error)
	assert.Nil(t, reloaded.NotifiedAt, "cleared so next failure fires fresh")

	// Success with no prior notified failure sends nothing.
	okRun2 := &database.SyncRun{TaskID: task.ID, Status: database.RunSuccess,
		Trigger: database.TriggerManual}
	require.NoError(t, db.Create(okRun2).Error)
	NotifySyncRunResolved(db, okRun2)
	assert.Len(t, payloads, 2)

	// skipped never alerts
	sk := &database.SyncRun{TaskID: task.ID, Status: database.RunSkipped,
		Trigger: database.TriggerManual}
	require.NoError(t, db.Create(sk).Error)
	NotifySyncRunFailure(db, sk)
	assert.Len(t, payloads, 2)
}
