package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"rclone_sync/internal/database"
	"rclone_sync/internal/services"
)

// --- per-resource RBAC (port of app/auth/deps.py datasource section) ---

// checkDSAccess: admin → allow; owner → full; binding rank ≥ level rank.
func (d *Deps) checkDSAccess(user *database.User, ds *database.DataSource, level string) bool {
	if user.Role == database.RoleAdmin {
		return true
	}
	if ds.OwnerUserID == user.ID {
		return true // owner has admin on their own resource
	}
	var b database.DataSourceBinding
	if err := d.DB.Where("data_source_id = ? AND user_id = ?", ds.ID, user.ID).First(&b).Error; err != nil {
		return false
	}
	return database.PermissionRank[b.Permission] >= database.PermissionRank[level]
}

const contextDataSourceKey = "dataSource"

// LoadDSForAccess loads the data source, 404s when missing, 403s when the
// user lacks the level, and stores the row for the handler.
func (d *Deps) LoadDSForAccess(level string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		var ds database.DataSource
		if err := d.DB.First(&ds, c.Param("data_source_id")).Error; err != nil {
			AbortErr(c, http.StatusNotFound, gin.H{"error": "not found", "resource": "data_source"})
			return
		}
		if !d.checkDSAccess(user, &ds, level) {
			AbortDetail(c, http.StatusForbidden, gin.H{
				"error": "forbidden", "level": level,
				"reason": "no binding to this data source",
			})
			return
		}
		c.Set(contextDataSourceKey, &ds)
		c.Next()
	}
}

func dsFrom(c *gin.Context) *database.DataSource {
	v, _ := c.Get(contextDataSourceKey)
	ds, _ := v.(*database.DataSource)
	return ds
}

// --- DTO ---

func toDataSourceOut(ds *database.DataSource) gin.H {
	return gin.H{
		"id": ds.ID, "name": ds.Name, "storage_source_id": ds.StorageSourceID,
		"path":          ds.Path,
		"access_key_id": strNil(ds.AccessKeyID), "secret_access_key": strNil(ds.SecretAccessKey),
		"description": strNil(ds.Description), "owner_user_id": ds.OwnerUserID,
		"last_verified_at": NaiveUTCPtr(ds.LastVerifiedAt), "last_verified_ok": boolNil(ds.LastVerifiedOK),
		"created_at": NaiveUTC(ds.CreatedAt), "updated_at": NaiveUTC(ds.UpdatedAt),
	}
}

func boolNil(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// enforceCredentials: AK/SK required unless the source type is local.
// Returns an error message or "".
func enforceCredentials(src *database.StorageSource, ak, sk *string) string {
	if src.Type == "local" {
		return ""
	}
	if (ak == nil || *ak == "") || (sk == nil || *sk == "") {
		return "storage source type '" + src.Type + "' requires access_key_id and secret_access_key"
	}
	return ""
}

// --- CRUD ---

// ListDataSources — admin sees all; others see owned + bound rows.
func (d *Deps) ListDataSources(c *gin.Context) {
	user := CurrentUser(c)
	q := d.DB.Model(&database.DataSource{})
	if user.Role != database.RoleAdmin {
		q = q.Where(
			"owner_user_id = ? OR id IN (SELECT data_source_id FROM data_source_bindings_v2 WHERE user_id = ?)",
			user.ID, user.ID,
		)
	}
	var rows []database.DataSource
	if err := q.Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toDataSourceOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

type dataSourceCreateIn struct {
	Name            string  `json:"name"`
	StorageSourceID int64   `json:"storage_source_id"`
	Path            string  `json:"path"`
	AccessKeyID     *string `json:"access_key_id"`
	SecretAccessKey *string `json:"secret_access_key"`
	Description     *string `json:"description"`
}

// CreateDataSource (leader+admin|edit) — owner is the current user. 201.
func (d *Deps) CreateDataSource(c *gin.Context) {
	var in dataSourceCreateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	v.Str("name", in.Name, StrOpt{Required: true, Min: 1, Max: 128})
	v.Str("path", in.Path, StrOpt{Required: true, Min: 1, Max: 512})
	if in.AccessKeyID != nil {
		v.Str("access_key_id", *in.AccessKeyID, StrOpt{Max: 255})
	}
	if in.SecretAccessKey != nil {
		v.Str("secret_access_key", *in.SecretAccessKey, StrOpt{Max: 512})
	}
	if in.Description != nil {
		v.Str("description", *in.Description, StrOpt{Max: 512})
	}
	if v.Abort(c) {
		return
	}

	var src database.StorageSource
	if err := d.DB.First(&src, in.StorageSourceID).Error; err != nil {
		AbortDetail(c, http.StatusBadRequest, "storage_source_id does not exist")
		return
	}
	if msg := enforceCredentials(&src, in.AccessKeyID, in.SecretAccessKey); msg != "" {
		AbortDetail(c, http.StatusBadRequest, msg)
		return
	}

	user := CurrentUser(c)
	ds := database.DataSource{
		Name: in.Name, StorageSourceID: in.StorageSourceID, Path: in.Path,
		AccessKeyID: in.AccessKeyID, SecretAccessKey: in.SecretAccessKey,
		Description: in.Description, OwnerUserID: user.ID,
	}
	if err := d.DB.Create(&ds).Error; err != nil {
		AbortDetail(c, http.StatusConflict,
			"data source name already exists for this owner: "+err.Error())
		return
	}
	c.JSON(http.StatusCreated, toDataSourceOut(&ds))
}

// GetDataSource — read access required.
func (d *Deps) GetDataSource(c *gin.Context) {
	user := CurrentUser(c)
	var ds database.DataSource
	if err := d.DB.First(&ds, c.Param("data_source_id")).Error; err != nil {
		AbortDetail(c, http.StatusNotFound, "data source not found")
		return
	}
	if !d.checkDSAccess(user, &ds, "read") {
		AbortDetail(c, http.StatusForbidden, gin.H{
			"error": "forbidden", "level": "read",
			"reason": "no access to this data source",
		})
		return
	}
	c.JSON(http.StatusOK, toDataSourceOut(&ds))
}

// UpdateDataSource — write access; partial update with post-state validation.
func (d *Deps) UpdateDataSource(c *gin.Context) {
	ds := dsFrom(c)
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
		ds.Name = *name
	}
	if path, ok, err := p.Str("path"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok && path != nil {
		v.Str("path", *path, StrOpt{Min: 1, Max: 512})
		ds.Path = *path
	}
	if desc, ok, err := p.Str("description"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if desc != nil {
			v.Str("description", *desc, StrOpt{Max: 512})
		}
		ds.Description = desc
	}
	if ak, ok, err := p.Str("access_key_id"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if ak != nil {
			v.Str("access_key_id", *ak, StrOpt{Max: 255})
		}
		ds.AccessKeyID = ak
	}
	if sk, ok, err := p.Str("secret_access_key"); err != nil {
		AbortInvalidJSON(c, err)
		return
	} else if ok {
		if sk != nil {
			v.Str("secret_access_key", *sk, StrOpt{Max: 512})
		}
		ds.SecretAccessKey = sk
	}

	newSrcID, hasSrc, err := p.Int64("storage_source_id")
	if err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	if hasSrc && newSrcID != nil {
		var src database.StorageSource
		if err := d.DB.First(&src, *newSrcID).Error; err != nil {
			AbortDetail(c, http.StatusBadRequest, "storage_source_id does not exist")
			return
		}
		ds.StorageSourceID = *newSrcID
		// Re-validate credentials against the post-update state.
		var src2 database.StorageSource
		d.DB.First(&src2, ds.StorageSourceID)
		if msg := enforceCredentials(&src2, ds.AccessKeyID, ds.SecretAccessKey); msg != "" {
			AbortDetail(c, http.StatusBadRequest, msg)
			return
		}
	} else if hasAKorSK(p) {
		var src database.StorageSource
		if err := d.DB.First(&src, ds.StorageSourceID).Error; err == nil {
			if msg := enforceCredentials(&src, ds.AccessKeyID, ds.SecretAccessKey); msg != "" {
				AbortDetail(c, http.StatusBadRequest, msg)
				return
			}
		}
	}
	if v.Abort(c) {
		return
	}

	if err := d.DB.Save(ds).Error; err != nil {
		AbortDetail(c, http.StatusConflict, "update conflict (likely duplicate name): "+err.Error())
		return
	}
	c.JSON(http.StatusOK, toDataSourceOut(ds))
}

func hasAKorSK(p Partial) bool {
	_, ak := p["access_key_id"]
	_, sk := p["secret_access_key"]
	return ak || sk
}

// DeleteDataSource — admin-level access; 409 while referenced by tasks.
func (d *Deps) DeleteDataSource(c *gin.Context) {
	ds := dsFrom(c)
	var used int64
	d.DB.Model(&database.SyncTask{}).Where(
		"src_data_source_id = ? OR dst_data_source_id = ?", ds.ID, ds.ID).Limit(1).Count(&used)
	if used > 0 {
		AbortDetail(c, http.StatusConflict, "data source is used by a sync task")
		return
	}
	d.DB.Model(&database.CheckTask{}).Where(
		"src_data_source_id = ? OR dst_data_source_id = ?", ds.ID, ds.ID).Limit(1).Count(&used)
	if used > 0 {
		AbortDetail(c, http.StatusConflict, "data source is used by a check task")
		return
	}
	d.DB.Delete(ds)
	c.Status(http.StatusNoContent)
}

// VerifyDataSource probes read+write access and updates last_verified_*.
func (d *Deps) VerifyDataSource(c *gin.Context) {
	ds := dsFrom(c)
	var src database.StorageSource
	if err := d.DB.First(&src, ds.StorageSourceID).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, "data source has no storage source")
		return
	}
	if d.RC == nil {
		AbortDetail(c, http.StatusServiceUnavailable, "rclone client not available")
		return
	}

	client := d.RC
	remoteName := services.DSRemoteName(ds)
	remoteSpec := remoteName + ":" + ds.Path
	var errMsg *string
	readOK, writeOK := false, false
	setErr := func(s string) { e := s; errMsg = &e }

	// Push the remote (idempotent); failure here is not fatal yet.
	if err := services.EnsureDataSourceRemote(d.DB, client, ds); err != nil {
		setErr("create_remote: " + err.Error())
	}
	if _, err := client.List(remoteSpec); err != nil {
		setErr("list: " + err.Error())
	} else {
		readOK = true
	}
	if readOK {
		ok, werr := client.WriteProbe(remoteSpec)
		writeOK = ok
		if werr != "" {
			tag := "write"
			if ok {
				tag = "write cleanup"
			}
			setErr(tag + ": " + werr)
		}
	}

	now := database.NowUTC()
	ds.LastVerifiedAt = &now
	allOK := readOK && writeOK && errMsg == nil
	ds.LastVerifiedOK = &allOK
	d.DB.Save(ds)

	c.JSON(http.StatusOK, gin.H{
		"read_ok": readOK, "write_ok": writeOK,
		"error": errMsg, "probed_at": NaiveUTC(now),
	})
}

// --- bindings ---

func toBindingOut(b *database.DataSourceBinding) gin.H {
	return gin.H{
		"id": b.ID, "data_source_id": b.DataSourceID, "user_id": b.UserID,
		"permission": b.Permission, "created_at": NaiveUTC(b.CreatedAt),
		"created_by_user_id": intNil(b.CreatedByUserID),
	}
}

func intNil(i *int64) any {
	if i == nil {
		return nil
	}
	return *i
}

// ListBindings — read access.
func (d *Deps) ListBindings(c *gin.Context) {
	var rows []database.DataSourceBinding
	if err := d.DB.Where("data_source_id = ?", c.Param("data_source_id")).Order("id").Find(&rows).Error; err != nil {
		AbortDetail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, toBindingOut(&rows[i]))
	}
	c.JSON(http.StatusOK, out)
}

type bindingCreateIn struct {
	UserID     int64  `json:"user_id"`
	Permission string `json:"permission"`
}

// CreateBinding — admin-level access. 201.
func (d *Deps) CreateBinding(c *gin.Context) {
	var in bindingCreateIn
	if err := c.ShouldBindJSON(&in); err != nil {
		AbortInvalidJSON(c, err)
		return
	}
	v := NewValidator()
	if v.OneOf("permission", in.Permission, database.PermissionRead, database.PermissionWrite, database.PermissionAdmin) {
		v.Abort(c)
		return
	}
	dsID, _ := parseID(c.Param("data_source_id"))
	var user database.User
	if err := d.DB.First(&user, in.UserID).Error; err != nil {
		AbortDetail(c, http.StatusBadRequest, "user_id does not exist")
		return
	}
	var count int64
	d.DB.Model(&database.DataSourceBinding{}).
		Where("data_source_id = ? AND user_id = ?", dsID, in.UserID).Count(&count)
	if count > 0 {
		AbortDetail(c, http.StatusConflict, "binding already exists")
		return
	}
	creator := CurrentUser(c)
	b := database.DataSourceBinding{
		DataSourceID: dsID, UserID: in.UserID, Permission: in.Permission,
	}
	if creator != nil {
		b.CreatedByUserID = &creator.ID
	}
	if err := d.DB.Create(&b).Error; err != nil {
		AbortDetail(c, http.StatusConflict, "binding already exists: "+err.Error())
		return
	}
	c.JSON(http.StatusCreated, toBindingOut(&b))
}

// UpdateBinding — admin-level access; changes permission only.
func (d *Deps) UpdateBinding(c *gin.Context) {
	var in struct {
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
	dsID, _ := parseID(c.Param("data_source_id"))
	bindingID, _ := parseID(c.Param("binding_id"))
	var b database.DataSourceBinding
	if err := d.DB.First(&b, bindingID).Error; err != nil || b.DataSourceID != dsID {
		AbortDetail(c, http.StatusNotFound, "binding not found")
		return
	}
	b.Permission = in.Permission
	d.DB.Save(&b)
	c.JSON(http.StatusOK, toBindingOut(&b))
}

// DeleteBinding — admin-level access.
func (d *Deps) DeleteBinding(c *gin.Context) {
	dsID, _ := parseID(c.Param("data_source_id"))
	bindingID, _ := parseID(c.Param("binding_id"))
	var b database.DataSourceBinding
	if err := d.DB.First(&b, bindingID).Error; err != nil || b.DataSourceID != dsID {
		AbortDetail(c, http.StatusNotFound, "binding not found")
		return
	}
	d.DB.Delete(&b)
	c.Status(http.StatusNoContent)
}

func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
