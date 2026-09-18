package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"clonectl/internal/database"
)

// toSyncTaskBindingOut and toCheckTaskBindingOut are the shared DTOs.
func toSyncTaskBindingOut(b *database.SyncTaskBinding) gin.H {
	return gin.H{
		"id": b.ID, "sync_task_id": b.SyncTaskID, "user_id": b.UserID,
		"permission": b.Permission, "created_at": NaiveUTC(b.CreatedAt),
		"created_by_user_id": intNil(b.CreatedByUserID),
	}
}

func toCheckTaskBindingOut(b *database.CheckTaskBinding) gin.H {
	return gin.H{
		"id": b.ID, "check_task_id": b.CheckTaskID, "user_id": b.UserID,
		"permission": b.Permission, "created_at": NaiveUTC(b.CreatedAt),
		"created_by_user_id": intNil(b.CreatedByUserID),
	}
}

// --- sync task bindings ---------------------------------------------------

func (d *Deps) ListSyncTaskBindings(c *gin.Context) {
	var rows []database.SyncTaskBinding
	if err := d.DB.Where("sync_task_id = ?", c.Param("task_id")).Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toSyncTaskBindingOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

func (d *Deps) CreateSyncTaskBinding(c *gin.Context) {
	var in struct {
		UserID     int64  `json:"user_id"`
		Permission string `json:"permission"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	if v.OneOf("permission", in.Permission, database.PermissionRead, database.PermissionWrite, database.PermissionAdmin) {
		v.Abort(c)
		return
	}
	taskID, _ := parseID(c.Param("task_id"))
	var u database.User
	if err := d.DB.First(&u, in.UserID).Error; err != nil {
		AbortDetail(c, http.StatusBadRequest, "user_id does not exist")
		return
	}
	var count int64
	d.DB.Model(&database.SyncTaskBinding{}).
		Where("sync_task_id = ? AND user_id = ?", taskID, in.UserID).Count(&count)
	if count > 0 {
		AbortDetail(c, http.StatusConflict, "binding already exists")
		return
	}
	creator := CurrentUser(c)
	b := database.SyncTaskBinding{
		SyncTaskID: taskID, UserID: in.UserID, Permission: in.Permission,
	}
	if creator != nil {
		b.CreatedByUserID = &creator.ID
	}
	if err := d.DB.Create(&b).Error; err != nil {
		AbortDetail(c, http.StatusConflict, "binding already exists: "+err.Error())
		return
	}
	c.JSON(http.StatusCreated, toSyncTaskBindingOut(&b))
}

func (d *Deps) UpdateSyncTaskBinding(c *gin.Context) {
	var in struct {
		Permission string `json:"permission"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	if !oneOf(in.Permission, database.PermissionRead, database.PermissionWrite, database.PermissionAdmin) {
		AbortDetail(c, http.StatusUnprocessableEntity, []ValidationError{
			{Loc: []any{"body", "permission"}, Msg: "permission must be read/write/admin", Type: "value_error"},
		})
		return
	}
	taskID, _ := parseID(c.Param("task_id"))
	bindingID, _ := parseID(c.Param("binding_id"))
	var b database.SyncTaskBinding
	if err := d.DB.First(&b, bindingID).Error; err != nil || b.SyncTaskID != taskID {
		AbortDetail(c, http.StatusNotFound, "binding not found")
		return
	}
	b.Permission = in.Permission
	d.DB.Save(&b)
	c.JSON(http.StatusOK, toSyncTaskBindingOut(&b))
}

func (d *Deps) DeleteSyncTaskBinding(c *gin.Context) {
	taskID, _ := parseID(c.Param("task_id"))
	bindingID, _ := parseID(c.Param("binding_id"))
	var b database.SyncTaskBinding
	if err := d.DB.First(&b, bindingID).Error; err != nil || b.SyncTaskID != taskID {
		AbortDetail(c, http.StatusNotFound, "binding not found")
		return
	}
	d.DB.Delete(&b)
	c.Status(http.StatusNoContent)
}

// --- check task bindings --------------------------------------------------

func (d *Deps) ListCheckTaskBindings(c *gin.Context) {
	var rows []database.CheckTaskBinding
	if err := d.DB.Where("check_task_id = ?", c.Param("check_task_id")).Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toCheckTaskBindingOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

func (d *Deps) CreateCheckTaskBinding(c *gin.Context) {
	var in struct {
		UserID     int64  `json:"user_id"`
		Permission string `json:"permission"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	if v.OneOf("permission", in.Permission, database.PermissionRead, database.PermissionWrite, database.PermissionAdmin) {
		v.Abort(c)
		return
	}
	taskID, _ := parseID(c.Param("check_task_id"))
	var u database.User
	if err := d.DB.First(&u, in.UserID).Error; err != nil {
		AbortDetail(c, http.StatusBadRequest, "user_id does not exist")
		return
	}
	var count int64
	d.DB.Model(&database.CheckTaskBinding{}).
		Where("check_task_id = ? AND user_id = ?", taskID, in.UserID).Count(&count)
	if count > 0 {
		AbortDetail(c, http.StatusConflict, "binding already exists")
		return
	}
	creator := CurrentUser(c)
	b := database.CheckTaskBinding{
		CheckTaskID: taskID, UserID: in.UserID, Permission: in.Permission,
	}
	if creator != nil {
		b.CreatedByUserID = &creator.ID
	}
	if err := d.DB.Create(&b).Error; err != nil {
		AbortDetail(c, http.StatusConflict, "binding already exists: "+err.Error())
		return
	}
	c.JSON(http.StatusCreated, toCheckTaskBindingOut(&b))
}

func (d *Deps) UpdateCheckTaskBinding(c *gin.Context) {
	var in struct {
		Permission string `json:"permission"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	if !oneOf(in.Permission, database.PermissionRead, database.PermissionWrite, database.PermissionAdmin) {
		AbortDetail(c, http.StatusUnprocessableEntity, []ValidationError{
			{Loc: []any{"body", "permission"}, Msg: "permission must be read/write/admin", Type: "value_error"},
		})
		return
	}
	taskID, _ := parseID(c.Param("check_task_id"))
	bindingID, _ := parseID(c.Param("binding_id"))
	var b database.CheckTaskBinding
	if err := d.DB.First(&b, bindingID).Error; err != nil || b.CheckTaskID != taskID {
		AbortDetail(c, http.StatusNotFound, "binding not found")
		return
	}
	b.Permission = in.Permission
	d.DB.Save(&b)
	c.JSON(http.StatusOK, toCheckTaskBindingOut(&b))
}

func (d *Deps) DeleteCheckTaskBinding(c *gin.Context) {
	taskID, _ := parseID(c.Param("check_task_id"))
	bindingID, _ := parseID(c.Param("binding_id"))
	var b database.CheckTaskBinding
	if err := d.DB.First(&b, bindingID).Error; err != nil || b.CheckTaskID != taskID {
		AbortDetail(c, http.StatusNotFound, "binding not found")
		return
	}
	d.DB.Delete(&b)
	c.Status(http.StatusNoContent)
}

func oneOf(s string, opts ...string) bool {
	for _, o := range opts {
		if s == o {
			return true
		}
	}
	return false
}