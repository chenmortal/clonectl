package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"rclone_sync/internal/database"
	"rclone_sync/internal/scheduler"
	"rclone_sync/internal/services"
)

// --- DTOs (JSON field names = FastAPI contract; *_storage_id always null in
// outputs because the redesigned task tables carry data-source refs only) ---

func toTaskOut(t *database.SyncTask, currentPerm string) gin.H {
	return gin.H{
		"id": t.ID, "name": t.Name,
		"src_data_source_id": t.SrcDataSourceID, "dst_data_source_id": t.DstDataSourceID,
		"src_storage_id": nil, "dst_storage_id": nil,
		"src_path": t.SrcPath, "dst_path": t.DstPath,
		"mode": t.Mode, "cron": t.Cron, "enabled": t.Enabled,
		"rclone_options": t.RcloneOptions, "pre_check_task_id": intNil(t.PreCheckTaskID),
		"creator_user_id": t.CreatorUserID,
		"created_at":      NaiveUTC(t.CreatedAt), "updated_at": NaiveUTC(t.UpdatedAt),
		"current_user_permission": currentPerm,
	}
}

func toRunOut(r *database.SyncRun) gin.H {
	var stats any
	if r.Stats != nil {
		stats = *r.Stats
	}
	return gin.H{
		"id": r.ID, "task_id": r.TaskID, "job_id": intNil(r.JobID),
		"status": r.Status, "trigger": r.Trigger,
		"started_at": NaiveUTCPtr(r.StartedAt), "finished_at": NaiveUTCPtr(r.FinishedAt),
		"error": strNil(r.Error), "stats": stats,
	}
}

func toCheckOut(r *database.CheckRun) gin.H {
	var result any
	if r.Result != nil {
		result = *r.Result
	}
	return gin.H{
		"id": r.ID, "task_id": r.TaskID, "job_id": intNil(r.JobID),
		"status": r.Status, "trigger": r.Trigger,
		"started_at": NaiveUTCPtr(r.StartedAt), "finished_at": NaiveUTCPtr(r.FinishedAt),
		"error": strNil(r.Error), "result": result,
	}
}

// --- schedule application (nil scheduler = skip, Python getattr parity) ---

func (d *Deps) applyTaskSchedule(t *database.SyncTask) {
	if d.Sched == nil {
		return
	}
	if t.Enabled {
		_ = d.Sched.RegisterTask(t)
	} else {
		d.Sched.RemoveTask(t.ID)
	}
}

func (d *Deps) applyCheckSchedule(t *database.CheckTask) {
	if d.Sched == nil {
		return
	}
	if t.Enabled {
		_ = d.Sched.RegisterCheckTask(t)
	} else {
		d.Sched.RemoveCheckTask(t.ID)
	}
}

// --- ref validation (Python _TaskCreateMixin + tasks._validate_refs) ---

// taskRefs holds the four possible references in wire form.
type taskRefs struct {
	SrcStorageID  *int64
	DstStorageID  *int64
	SrcDataSource *int64
	DstDataSource *int64
}

// resolveRefs enforces the pairing invariant and translates legacy storage
// ids through migration_log into data-source ids (the redesigned tables
// carry data-source refs only).
func (d *Deps) resolveRefs(refs taskRefs) (srcDS, dstDS int64, httpStatus int, msg string) {
	if refs.SrcStorageID != nil && refs.SrcDataSource != nil {
		return 0, 0, 0, "src: set either storage_id or data_source_id, not both"
	}
	if refs.DstStorageID != nil && refs.DstDataSource != nil {
		return 0, 0, 0, "dst: set either storage_id or data_source_id, not both"
	}
	newSet := refs.SrcDataSource != nil && refs.DstDataSource != nil
	legacySet := refs.SrcStorageID != nil && refs.DstStorageID != nil
	if !newSet && !legacySet {
		return 0, 0, 0, "must set both src_data_source_id and dst_data_source_id (new) or both src_storage_id and dst_storage_id (legacy)"
	}
	if newSet && legacySet {
		return 0, 0, 0, "set either the new data_source_id pair or the legacy storage_id pair, not both"
	}

	if newSet {
		for _, id := range []int64{*refs.SrcDataSource, *refs.DstDataSource} {
			var n int64
			d.DB.Model(&database.DataSource{}).Where("id = ?", id).Count(&n)
			if n == 0 {
				return 0, 0, http.StatusBadRequest, fmt.Sprintf("data source %d not found", id)
			}
		}
		return *refs.SrcDataSource, *refs.DstDataSource, 0, ""
	}

	// Legacy pair: row must exist AND be migrated (we can only reference
	// data sources in the redesigned schema).
	for _, id := range []int64{*refs.SrcStorageID, *refs.DstStorageID} {
		var cfg database.StorageConfig
		if err := d.DB.First(&cfg, id).Error; err != nil {
			return 0, 0, http.StatusBadRequest, fmt.Sprintf("storage %d not found", id)
		}
		var log database.MigrationLog
		if err := d.DB.First(&log, "table_name = ? AND legacy_id = ?", "storage_config", id).Error; err != nil {
			return 0, 0, http.StatusBadRequest,
				fmt.Sprintf("storage %d not found", id)
		}
	}
	var srcLog, dstLog database.MigrationLog
	d.DB.First(&srcLog, "table_name = ? AND legacy_id = ?", "storage_config", *refs.SrcStorageID)
	d.DB.First(&dstLog, "table_name = ? AND legacy_id = ?", "storage_config", *refs.DstStorageID)
	return srcLog.NewID, dstLog.NewID, 0, ""
}

func (d *Deps) validatePreCheck(preCheckTaskID int64) (int, string) {
	var n int64
	d.DB.Model(&database.CheckTask{}).Where("id = ?", preCheckTaskID).Count(&n)
	if n == 0 {
		return http.StatusBadRequest, fmt.Sprintf("check task %d not found", preCheckTaskID)
	}
	return 0, ""
}

// --- handlers ---

// ListTasks — admin sees all; others see only tasks they have a binding for.
func (d *Deps) ListTasks(c *gin.Context) {
	user := CurrentUser(c)
	q := d.DB.Model(&database.SyncTask{})
	if ids, all := d.visibleSyncTaskIDs(user); !all {
		if len(ids) == 0 {
			c.JSON(http.StatusOK, []gin.H{})
			return
		}
		q = q.Where("id IN ?", ids)
	}
	var rows []database.SyncTask
	if err := q.Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	perms := d.syncTaskPermissionLookups(user, ids)
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toTaskOut(&rows[i], perms[rows[i].ID]))
	}
	c.JSON(http.StatusOK, out)
}

type taskCreateIn struct {
	Name            string         `json:"name"`
	SrcDataSourceID *int64         `json:"src_data_source_id"`
	DstDataSourceID *int64         `json:"dst_data_source_id"`
	SrcStorageID    *int64         `json:"src_storage_id"`
	DstStorageID    *int64         `json:"dst_storage_id"`
	SrcPath         string         `json:"src_path"`
	DstPath         string         `json:"dst_path"`
	Mode            string         `json:"mode"`
	Cron            string         `json:"cron"`
	Enabled         *bool          `json:"enabled"`
	RcloneOptions   map[string]any `json:"rclone_options"`
	PreCheckTaskID  *int64         `json:"pre_check_task_id"`
}

// CreateTask (leader+admin|edit) → 201. Inserts a creator admin
// SyncTaskBinding in the same transaction so the creator has admin access
// by default and a global admin can revoke that row later.
func (d *Deps) CreateTask(c *gin.Context) {
	var in taskCreateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("name", in.Name, StrOpt{Required: true, Min: 1, Max: 128})
	if in.Mode != "" {
		v.OneOf("mode", in.Mode, database.ModeSync, database.ModeCopy)
	} else {
		v.add("mode", "Field required", "missing")
	}
	v.Str("cron", in.Cron, StrOpt{Required: true})
	if v.Abort(c) {
		return
	}
	if err := schedulerValidateCron(in.Cron); err != nil {
		AbortDetail(c, http.StatusUnprocessableEntity, []ValidationError{
			{Loc: []any{"body", "cron"}, Msg: err.Error(), Type: "value_error"},
		})
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
	if in.PreCheckTaskID != nil {
		if status, msg := d.validatePreCheck(*in.PreCheckTaskID); status != 0 {
			AbortDetail(c, status, msg)
			return
		}
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	options := in.RcloneOptions
	if options == nil {
		options = map[string]any{}
	}
	user := CurrentUser(c)
	task := database.SyncTask{
		Name: in.Name, SrcDataSourceID: srcDS, DstDataSourceID: dstDS,
		SrcPath: in.SrcPath, DstPath: in.DstPath, Mode: in.Mode, Cron: in.Cron,
		Enabled: enabled, RcloneOptions: database.JSONObject(options),
		PreCheckTaskID: in.PreCheckTaskID,
		CreatorUserID:  user.ID,
	}
	err := d.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		syncID := task.ID
		return d.insertCreatorBindings(tx, user.ID, &syncID, nil)
	})
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	d.applyTaskSchedule(&task)
	c.JSON(http.StatusCreated, toTaskOut(&task, database.PermissionAdmin))
}

// GetTask — read access (gated by LoadSyncTaskForAccess).

// GetTask — read access (gated by LoadSyncTaskForAccess).
func (d *Deps) GetTask(c *gin.Context) {
	t := syncTaskFrom(c)
	perm := d.syncTaskPermissionLookups(CurrentUser(c), []int64{t.ID})[t.ID]
	c.JSON(http.StatusOK, toTaskOut(t, perm))
}

// UpdateTask (leader+admin|edit, write access via LoadSyncTaskForAccess) —
// partial update, merged ref validation, schedule re-applied.
func (d *Deps) UpdateTask(c *gin.Context) {
	taskPtr := syncTaskFrom(c)
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
	if mode, ok, err := p.Str("mode"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && mode != nil {
		if v.OneOf("mode", *mode, database.ModeSync, database.ModeCopy) {
			v.Abort(c)
			return
		}
		task.Mode = *mode
	}
	if cron, ok, err := p.Str("cron"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && cron != nil {
		if err := schedulerValidateCron(*cron); err != nil {
			AbortDetail(c, http.StatusUnprocessableEntity, []ValidationError{
				{Loc: []any{"body", "cron"}, Msg: err.Error(), Type: "value_error"},
			})
			return
		}
		task.Cron = *cron
	}
	if enabled, ok, err := p.Bool("enabled"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && enabled != nil {
		task.Enabled = *enabled
	}
	if options, ok, err := p.Object("rclone_options"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && options != nil {
		task.RcloneOptions = database.JSONObject(options)
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
	if preCheck, ok, err := p.Int64("pre_check_task_id"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if preCheck != nil {
			if status, msg := d.validatePreCheck(*preCheck); status != 0 {
				AbortDetail(c, status, msg)
				return
			}
		}
		task.PreCheckTaskID = preCheck
	}

	task.SrcDataSourceID = srcDSID
	task.DstDataSourceID = dstDSID
	if v.Abort(c) {
		return
	}
	if err := d.DB.Save(&task).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	d.applyTaskSchedule(&task)
	perm := d.syncTaskPermissionLookups(CurrentUser(c), []int64{task.ID})[task.ID]
	c.JSON(http.StatusOK, toTaskOut(&task, perm))
}

// DeleteTask (leader+admin|edit, admin access via LoadSyncTaskForAccess) —
// schedule removed first, runs + bindings cascade.
func (d *Deps) DeleteTask(c *gin.Context) {
	task := syncTaskFrom(c)
	if d.Sched != nil {
		d.Sched.RemoveTask(task.ID)
	}
	d.DB.Where("task_id = ?", task.ID).Delete(&database.SyncRun{})
	d.DB.Where("sync_task_id = ?", task.ID).Delete(&database.SyncTaskBinding{})
	d.DB.Delete(task)
	c.Status(http.StatusNoContent)
}

// TriggerTask (write access via LoadSyncTaskForAccess, no leader gate) → 202 + RunOut.
func (d *Deps) TriggerTask(c *gin.Context) {
	task := syncTaskFrom(c)
	if d.RC == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "rclone client not available")
		return
	}
	run, err := services.RunTask(d.DB, d.RC, d.Cfg.CheckTimeout, task.ID, database.TriggerManual)
	if err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusAccepted, toRunOut(run))
}

// schedulerValidateCron bridges to the scheduler package's validator.
func schedulerValidateCron(expr string) error { return scheduler.ValidateCron(expr) }

// statusOr422 maps unmarked statuses to the validation status.
func statusOr422(status int) int {
	if status == 0 {
		return http.StatusUnprocessableEntity
	}
	return status
}
