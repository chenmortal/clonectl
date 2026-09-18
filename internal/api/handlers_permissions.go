package api

import (
	"clonectl/internal/database"
)

// CurrentPermission returns the caller's effective permission on a single
// resource. Global admins always get "admin". The empty string means the
// caller has no binding and the API will return 403 on every action.
func (d *Deps) CurrentPermission(user *database.User, resourceID int64, lookUp func(userID int64) string) string {
	if user == nil {
		return ""
	}
	if user.Role == database.RoleAdmin {
		return database.PermissionAdmin
	}
	return lookUp(user.ID)
}

// dsPermissionLookups returns a per-id permission lookup for a list of
// data source ids, batched into a single IN query.
func (d *Deps) dsPermissionLookups(user *database.User, ids []int64) map[int64]string {
	if user == nil || user.Role == database.RoleAdmin {
		return adminMap(ids, database.PermissionAdmin)
	}
	return d.bindingPermissionMap("data_source_id", ids, user.ID)
}

// syncTaskPermissionLookups — same, for sync tasks.
func (d *Deps) syncTaskPermissionLookups(user *database.User, ids []int64) map[int64]string {
	if user == nil || user.Role == database.RoleAdmin {
		return adminMap(ids, database.PermissionAdmin)
	}
	return d.bindingPermissionMap("sync_task_id", ids, user.ID)
}

// checkTaskPermissionLookups — same, for check tasks.
func (d *Deps) checkTaskPermissionLookups(user *database.User, ids []int64) map[int64]string {
	if user == nil || user.Role == database.RoleAdmin {
		return adminMap(ids, database.PermissionAdmin)
	}
	return d.bindingPermissionMap("check_task_id", ids, user.ID)
}

// bindingPermissionMap queries <table> bindings for the given resource ids
// + user, returns {resource_id: permission}. Missing rows map to "".
func (d *Deps) bindingPermissionMap(resourceCol string, ids []int64, userID int64) map[int64]string {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	type row struct {
		ResourceID int64
		Permission string
	}
	var rows []row
	q := d.DB.Table("data_source_bindings_v2")
	switch resourceCol {
	case "sync_task_id":
		q = d.DB.Table("sync_task_bindings_v2")
	case "check_task_id":
		q = d.DB.Table("check_task_bindings_v2")
	}
	if err := q.Select(resourceCol+" AS resource_id, permission").
		Where(resourceCol+" IN ? AND user_id = ?", ids, userID).
		Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.ResourceID] = r.Permission
	}
	return out
}

func adminMap(ids []int64, perm string) map[int64]string {
	out := make(map[int64]string, len(ids))
	for _, id := range ids {
		out[id] = perm
	}
	return out
}
