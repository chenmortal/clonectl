package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"rclone_sync/internal/database"
)

// --- sync-task RBAC --------------------------------------------------------

// checkSyncTaskAccess: global admin → allow; otherwise look up a
// SyncTaskBinding row. The creator's admin row is inserted on Create; that
// row is the only thing granting non-admin callers access and can be revoked
// like any other binding.
func (d *Deps) checkSyncTaskAccess(user *database.User, task *database.SyncTask, level string) bool {
	if user.Role == database.RoleAdmin {
		return true
	}
	var b database.SyncTaskBinding
	if err := d.DB.Where("sync_task_id = ? AND user_id = ?", task.ID, user.ID).First(&b).Error; err != nil {
		return false
	}
	return database.PermissionRank[b.Permission] >= database.PermissionRank[level]
}

const contextSyncTaskKey = "syncTask"

// LoadSyncTaskForAccess loads the sync task, 404s when missing, 403s when the
// user lacks the level, and stores the row for the handler.
func (d *Deps) LoadSyncTaskForAccess(level string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		var task database.SyncTask
		if err := d.DB.First(&task, c.Param("task_id")).Error; err != nil {
			AbortDetail(c, http.StatusNotFound, "task not found")
			return
		}
		if !d.checkSyncTaskAccess(user, &task, level) {
			AbortDetail(c, http.StatusForbidden, gin.H{
				"error": "forbidden", "level": level,
				"reason": "no binding to this task",
			})
			return
		}
		c.Set(contextSyncTaskKey, &task)
		c.Next()
	}
}

func syncTaskFrom(c *gin.Context) *database.SyncTask {
	v, _ := c.Get(contextSyncTaskKey)
	t, _ := v.(*database.SyncTask)
	return t
}

// visibleSyncTaskIDs returns the set of sync task ids the user can see. Used
// to filter ListTasks / ListRuns. Admin → nil (no filter).
func (d *Deps) visibleSyncTaskIDs(user *database.User) ([]int64, bool) {
	if user.Role == database.RoleAdmin {
		return nil, true
	}
	var ids []int64
	d.DB.Model(&database.SyncTaskBinding{}).
		Where("user_id = ?", user.ID).
		Pluck("sync_task_id", &ids)
	return ids, false
}

// --- check-task RBAC --------------------------------------------------------

func (d *Deps) checkCheckTaskAccess(user *database.User, task *database.CheckTask, level string) bool {
	if user.Role == database.RoleAdmin {
		return true
	}
	var b database.CheckTaskBinding
	if err := d.DB.Where("check_task_id = ? AND user_id = ?", task.ID, user.ID).First(&b).Error; err != nil {
		return false
	}
	return database.PermissionRank[b.Permission] >= database.PermissionRank[level]
}

const contextCheckTaskKey = "checkTask"

func (d *Deps) LoadCheckTaskForAccess(level string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		var task database.CheckTask
		if err := d.DB.First(&task, c.Param("check_task_id")).Error; err != nil {
			AbortDetail(c, http.StatusNotFound, "check task not found")
			return
		}
		if !d.checkCheckTaskAccess(user, &task, level) {
			AbortDetail(c, http.StatusForbidden, gin.H{
				"error": "forbidden", "level": level,
				"reason": "no binding to this check task",
			})
			return
		}
		c.Set(contextCheckTaskKey, &task)
		c.Next()
	}
}

func checkTaskFrom(c *gin.Context) *database.CheckTask {
	v, _ := c.Get(contextCheckTaskKey)
	t, _ := v.(*database.CheckTask)
	return t
}

func (d *Deps) visibleCheckTaskIDs(user *database.User) ([]int64, bool) {
	if user.Role == database.RoleAdmin {
		return nil, true
	}
	var ids []int64
	d.DB.Model(&database.CheckTaskBinding{}).
		Where("user_id = ?", user.ID).
		Pluck("check_task_id", &ids)
	return ids, false
}

// insertCreatorBindings writes admin bindings for the creator for both sync
// and check task ids when provided. Used by create handlers so the creator
// starts with admin access.
func (d *Deps) insertCreatorBindings(tx *gorm.DB, creatorID int64, syncTaskID, checkTaskID *int64) error {
	if syncTaskID != nil {
		id := creatorID
		b := database.SyncTaskBinding{
			SyncTaskID: *syncTaskID, UserID: creatorID, Permission: database.PermissionAdmin,
			CreatedByUserID: &id,
		}
		if err := tx.Create(&b).Error; err != nil {
			return err
		}
	}
	if checkTaskID != nil {
		id := creatorID
		b := database.CheckTaskBinding{
			CheckTaskID: *checkTaskID, UserID: creatorID, Permission: database.PermissionAdmin,
			CreatedByUserID: &id,
		}
		if err := tx.Create(&b).Error; err != nil {
			return err
		}
	}
	return nil
}