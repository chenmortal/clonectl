package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"clonectl/internal/database"
	"clonectl/internal/services"
)

func toCheckTaskOut(t *database.CheckTask, currentPerm string) gin.H {
	return gin.H{
		"id": t.ID, "name": t.Name,
		"src_data_source_id": t.SrcDataSourceID, "dst_data_source_id": t.DstDataSourceID,
		"src_storage_id": nil, "dst_storage_id": nil,
		"src_path": t.SrcPath, "dst_path": t.DstPath,
		"cron": strNil(t.Cron), "enabled": t.Enabled,
		"check_options": t.CheckOptions,
		"creator_user_id": t.CreatorUserID,
		"created_at":      NaiveUTC(t.CreatedAt), "updated_at": NaiveUTC(t.UpdatedAt),
		"current_user_permission": currentPerm,
	}
}

// ListCheckTasks — admin sees all; others see only tasks they have a binding for.
func (d *Deps) ListCheckTasks(c *gin.Context) {
	user := CurrentUser(c)
	q := d.DB.Model(&database.CheckTask{})
	if ids, all := d.visibleCheckTaskIDs(user); !all {
		if len(ids) == 0 {
			c.JSON(http.StatusOK, []gin.H{})
			return
		}
		q = q.Where("id IN ?", ids)
	}
	var rows []database.CheckTask
	if err := q.Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	perms := d.checkTaskPermissionLookups(user, ids)
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toCheckTaskOut(&rows[i], perms[rows[i].ID]))
	}
	c.JSON(http.StatusOK, out)
}

type checkTaskCreateIn struct {
	Name            string         `json:"name"`
	SrcDataSourceID *int64         `json:"src_data_source_id"`
	DstDataSourceID *int64         `json:"dst_data_source_id"`
	SrcStorageID    *int64         `json:"src_storage_id"`
	DstStorageID    *int64         `json:"dst_storage_id"`
	SrcPath         string         `json:"src_path"`
	DstPath         string         `json:"dst_path"`
	Cron            *string        `json:"cron"`
	Enabled         *bool          `json:"enabled"`
	CheckOptions    map[string]any `json:"check_options"`
}

// CreateCheckTask (leader+admin|edit) → 201. Inserts a creator admin
// CheckTaskBinding in the same transaction.
func (d *Deps) CreateCheckTask(c *gin.Context) {
	var in checkTaskCreateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("name", in.Name, StrOpt{Required: true, Min: 1, Max: 128})
	if in.Cron != nil && *in.Cron != "" {
		if err := schedulerValidateCron(*in.Cron); err != nil {
			AbortDetail(c, http.StatusUnprocessableEntity, []ValidationError{
				{Loc: []any{"body", "cron"}, Msg: err.Error(), Type: "value_error"},
			})
			return
		}
	}
	if v.Abort(c) {
		return
	}

	refs := taskRefs{SrcStorageID: in.SrcStorageID, DstStorageID: in.DstStorageID,
		SrcDataSource: in.SrcDataSourceID, DstDataSource: in.DstDataSourceID}
	srcDS, dstDS, status, msg := d.resolveRefs(refs)
	if msg != "" {
		if status == http.StatusBadRequest {
			AbortDetail(c, status, msg)
		} else {
			AbortDetail(c, statusOr422(status), []ValidationError{
				{Loc: []any{"body"}, Msg: msg, Type: "value_error"},
			})
		}
		return
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	options := in.CheckOptions
	if options == nil {
		options = map[string]any{}
	}
	user := CurrentUser(c)
	task := database.CheckTask{
		Name: in.Name, SrcDataSourceID: srcDS, DstDataSourceID: dstDS,
		SrcPath: in.SrcPath, DstPath: in.DstPath, Cron: in.Cron,
		Enabled: enabled, CheckOptions: database.JSONObject(options),
		CreatorUserID: user.ID,
	}
	err := d.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		checkID := task.ID
		return d.insertCreatorBindings(tx, user.ID, nil, &checkID)
	})
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	d.applyCheckSchedule(&task)
	c.JSON(http.StatusCreated, toCheckTaskOut(&task, database.PermissionAdmin))
}

// GetCheckTask — read access (gated by LoadCheckTaskForAccess).
func (d *Deps) GetCheckTask(c *gin.Context) {
	t := checkTaskFrom(c)
	perm := d.checkTaskPermissionLookups(CurrentUser(c), []int64{t.ID})[t.ID]
	c.JSON(http.StatusOK, toCheckTaskOut(t, perm))
}

// UpdateCheckTask (leader+admin|edit, write access via LoadCheckTaskForAccess) — partial update.
func (d *Deps) UpdateCheckTask(c *gin.Context) {
	taskPtr := checkTaskFrom(c)
	task := *taskPtr
	raw, err := c.GetRawData()
	if err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	p, err := DecodePartial(raw)
	if err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()

	if name, ok, err := p.Str("name"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && name != nil {
		v.Str("name", *name, StrOpt{Min: 1, Max: 128})
		task.Name = *name
	}
	if srcPath, ok, err := p.Str("src_path"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && srcPath != nil {
		task.SrcPath = *srcPath
	}
	if dstPath, ok, err := p.Str("dst_path"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && dstPath != nil {
		task.DstPath = *dstPath
	}
	if cron, ok, err := p.Str("cron"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if cron != nil && *cron != "" {
			if err := schedulerValidateCron(*cron); err != nil {
				AbortDetail(c, http.StatusUnprocessableEntity, []ValidationError{
					{Loc: []any{"body", "cron"}, Msg: err.Error(), Type: "value_error"},
				})
				return
			}
		}
		task.Cron = cron
	}
	if enabled, ok, err := p.Bool("enabled"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && enabled != nil {
		task.Enabled = *enabled
	}
	if options, ok, err := p.Object("check_options"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && options != nil {
		task.CheckOptions = database.JSONObject(options)
	}

	refs := taskRefs{
		SrcDataSource: &task.SrcDataSourceID,
		DstDataSource: &task.DstDataSourceID,
	}
	if srcStorage, ok, err := p.Int64("src_storage_id"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		refs.SrcStorageID = srcStorage
	}
	if dstStorage, ok, err := p.Int64("dst_storage_id"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		refs.DstStorageID = dstStorage
	}
	if srcDS, ok, err := p.Int64("src_data_source_id"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && srcDS != nil {
		refs.SrcDataSource = srcDS
	}
	if dstDS, ok, err := p.Int64("dst_data_source_id"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && dstDS != nil {
		refs.DstDataSource = dstDS
	}
	srcDSID, dstDSID, status, msg := d.resolveRefs(refs)
	if msg != "" {
		if status == http.StatusBadRequest {
			AbortDetail(c, status, msg)
		} else {
			AbortDetail(c, statusOr422(status), []ValidationError{
				{Loc: []any{"body"}, Msg: msg, Type: "value_error"},
			})
		}
		return
	}
	if v.Abort(c) {
		return
	}
	task.SrcDataSourceID = srcDSID
	task.DstDataSourceID = dstDSID
	if err := d.DB.Save(&task).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	d.applyCheckSchedule(&task)
	perm := d.checkTaskPermissionLookups(CurrentUser(c), []int64{task.ID})[task.ID]
	c.JSON(http.StatusOK, toCheckTaskOut(&task, perm))
}

// DeleteCheckTask (leader+admin|edit, admin access via LoadCheckTaskForAccess)
// — 409 while referenced as pre-check; bindings cascade.
func (d *Deps) DeleteCheckTask(c *gin.Context) {
	task := checkTaskFrom(c)
	var used int64
	d.DB.Model(&database.SyncTask{}).Where("pre_check_task_id = ?", task.ID).Limit(1).Count(&used)
	if used > 0 {
		AbortDetail(c, http.StatusConflict, "check task is used as pre-check of a sync task")
		return
	}
	if d.Sched != nil {
		d.Sched.RemoveCheckTask(task.ID)
	}
	d.DB.Where("task_id = ?", task.ID).Delete(&database.CheckRun{})
	d.DB.Where("check_task_id = ?", task.ID).Delete(&database.CheckTaskBinding{})
	d.DB.Delete(task)
	c.Status(http.StatusNoContent)
}

// TriggerCheckTask (write access via LoadCheckTaskForAccess) → 202 + CheckOut.
func (d *Deps) TriggerCheckTask(c *gin.Context) {
	task := checkTaskFrom(c)
	if d.RC == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "rclone client not available")
		return
	}
	check, err := services.RunCheck(d.DB, d.RC, task.ID, database.TriggerManual)
	if err != nil {
		if fmt.Sprintf("%v", err) == fmt.Sprintf("check task %d not found", task.ID) {
			AbortDetail(c, http.StatusNotFound, "check task not found")
			return
		}
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusAccepted, toCheckOut(check))
}
