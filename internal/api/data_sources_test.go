package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
	"rclone_sync/internal/rclone/rctest"
)

// seedDSEnv creates admin+edit+view users, one s3 storage source, and wires a
// fake rclone client. Returns everything the tests need.
func seedDSEnv(t *testing.T) (*Deps, *gin.Engine, string, string, string, *database.StorageSource, *rctest.Server) {
	t.Helper()
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	mkUser(t, d, "ed", "edit")
	mkUser(t, d, "viewer", "view")
	admin := login(t, r, "root", "pass1234")
	edit := login(t, r, "ed", "pass1234")
	view := login(t, r, "viewer", "pass1234")

	src := database.StorageSource{
		Name: "minio-main", Type: "s3", Extra: database.JSONObject{"provider": "Minio"},
	}
	require.NoError(t, d.DB.Create(&src).Error)

	srv := rctest.New()
	t.Cleanup(srv.Close)
	d.RC = rclone.NewClient(srv.URL(), "u", "p", 0)
	return d, r, admin, edit, view, &src, srv
}

func createDS(t *testing.T, r *gin.Engine, token string, body map[string]any) map[string]any {
	t.Helper()
	w := doJSON(r, http.MethodPost, "/api/data-sources", token, body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	return out
}

func TestDataSourceCRUD(t *testing.T) {
	d, r, admin, _, view, src, _ := seedDSEnv(t)

	ds := createDS(t, r, admin, map[string]any{
		"name": "bucket-a", "storage_source_id": src.ID, "path": "/data",
		"access_key_id": "ak", "secret_access_key": "sk",
	})
	dsID := int(ds["id"].(float64))
	// Creator receives an admin binding by default.
	var bs []database.DataSourceBinding
	require.NoError(t, d.DB.Where("data_source_id = ? AND user_id = ?", dsID, 1).Find(&bs).Error)
	require.Len(t, bs, 1)
	assert.Equal(t, database.PermissionAdmin, bs[0].Permission)

	// Missing AK/SK on s3 → 400
	w := doJSON(r, http.MethodPost, "/api/data-sources", admin, map[string]any{
		"name": "no-creds", "storage_source_id": src.ID, "path": "/x",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "requires access_key_id")

	// Bad FK → 400
	w = doJSON(r, http.MethodPost, "/api/data-sources", admin, map[string]any{
		"name": "bad-fk", "storage_source_id": 999, "path": "/x",
		"access_key_id": "a", "secret_access_key": "b",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "does not exist")

	// Duplicate name for same owner → 409
	w = doJSON(r, http.MethodPost, "/api/data-sources", admin, map[string]any{
		"name": "bucket-a", "storage_source_id": src.ID, "path": "/data",
		"access_key_id": "ak", "secret_access_key": "sk",
	})
	assert.Equal(t, http.StatusConflict, w.Code)

	// Same name, different owner (edit) → OK
	_ = createDS(t, r, mkEditToken(t, d, r), map[string]any{
		"name": "bucket-a", "storage_source_id": src.ID, "path": "/data",
		"access_key_id": "ak", "secret_access_key": "sk",
	})

	// view role cannot create
	w = doJSON(r, http.MethodPost, "/api/data-sources", view, map[string]any{
		"name": "v1", "storage_source_id": src.ID, "path": "/x",
		"access_key_id": "a", "secret_access_key": "b",
	})
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Get — view has no access → 403
	w = doJSON(r, http.MethodGet, "/api/data-sources/"+itoa(dsID), view, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// List — admin sees all, view sees none
	w = doJSON(r, http.MethodGet, "/api/data-sources", admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var rows []map[string]any
	require.NoError(t, unmarshalBody(w, &rows))
	assert.Len(t, rows, 2)
	// Every row reports the caller's permission (admin → "admin").
	for _, r := range rows {
		assert.Equal(t, database.PermissionAdmin, r["current_user_permission"], "admin sees admin perm")
	}
	w = doJSON(r, http.MethodGet, "/api/data-sources", view, nil)
	require.NoError(t, unmarshalBody(w, &rows))
	assert.Empty(t, rows)

	// Delete referenced-by-nothing → 204
	w = doJSON(r, http.MethodDelete, "/api/data-sources/"+itoa(dsID), admin, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func mkEditToken(t *testing.T, d *Deps, r *gin.Engine) string {
	t.Helper()
	var u database.User
	require.NoError(t, d.DB.Where("username = ?", "ed").First(&u).Error)
	_ = u
	return login(t, r, "ed", "pass1234")
}

func TestDataSourceUpdateAndLocalBackend(t *testing.T) {
	d, r, admin, _, _, src, _ := seedDSEnv(t)

	// local-type source: no credentials required
	local := database.StorageSource{Name: "fs", Type: "local", Extra: database.JSONObject{}}
	require.NoError(t, d.DB.Create(&local).Error)
	ds := createDS(t, r, admin, map[string]any{
		"name": "local-ds", "storage_source_id": local.ID, "path": "/var/data",
	})
	dsID := int(ds["id"].(float64))
	assert.Nil(t, ds["access_key_id"])

	// Update: clear credentials on s3-backed ds → 400 (post-state validation)
	ds2 := createDS(t, r, admin, map[string]any{
		"name": "s3-ds", "storage_source_id": src.ID, "path": "/x",
		"access_key_id": "ak", "secret_access_key": "sk",
	})
	ds2ID := int(ds2["id"].(float64))
	w := doJSON(r, http.MethodPut, "/api/data-sources/"+itoa(ds2ID), admin,
		map[string]any{"access_key_id": nil, "secret_access_key": nil})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Update: switch s3-ds to local source → OK (local needs no creds)
	w = doJSON(r, http.MethodPut, "/api/data-sources/"+itoa(ds2ID), admin,
		map[string]any{"storage_source_id": local.ID})
	assert.Equal(t, http.StatusOK, w.Code)

	// Partial update keeps other fields
	w = doJSON(r, http.MethodPut, "/api/data-sources/"+itoa(dsID), admin,
		map[string]any{"description": "main disk"})
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "main disk", out["description"])
	assert.Equal(t, "/var/data", out["path"])
	_ = dsID
}

func TestDataSourceVerify(t *testing.T) {
	d, r, admin, _, view, src, srv := seedDSEnv(t)
	ds := createDS(t, r, admin, map[string]any{
		"name": "v", "storage_source_id": src.ID, "path": "/data",
		"access_key_id": "ak", "secret_access_key": "sk",
	})
	dsID := itoa(int(ds["id"].(float64)))

	// Success path: read list + write probe + cleanup all OK
	w := doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/verify", admin, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, true, out["read_ok"])
	assert.Equal(t, true, out["write_ok"])
	assert.Nil(t, out["error"])
	assert.NotNil(t, out["probed_at"])

	// last_verified_* persisted
	var row database.DataSource
	require.NoError(t, d.DB.First(&row, ds["id"]).Error)
	require.NotNil(t, row.LastVerifiedOK)
	assert.True(t, *row.LastVerifiedOK)
	// remote pushed as ds-{id}
	assert.Equal(t, 1, srv.Count("/config/create"))

	// Read failure
	srv.FailListNext(500, "no such bucket")
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/verify", admin, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, false, out["read_ok"])
	assert.Equal(t, false, out["write_ok"])
	assert.Contains(t, out["error"], "list:")

	// Write failure (copyfile fails)
	srv.FailCopyNext(500, "readonly fs")
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/verify", admin, nil)
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, true, out["read_ok"])
	assert.Equal(t, false, out["write_ok"])
	assert.Contains(t, out["error"], "write:")

	// Verify is observational — a read-only binding is enough.
	// Grant view a read binding and re-verify.
	var ed database.User
	require.NoError(t, d.DB.Where("username = ?", "ed").First(&ed).Error)
	require.NoError(t, d.DB.Create(&database.DataSourceBinding{
		DataSourceID: int64(ds["id"].(float64)),
		UserID:       ed.ID,
		Permission:   database.PermissionRead,
	}).Error)
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/verify", login(t, r, "ed", "pass1234"), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// A user with no binding at all still gets 403.
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/verify", view, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDataSourceBindings(t *testing.T) {
	d, r, admin, _, _, src, _ := seedDSEnv(t)
	ds := createDS(t, r, admin, map[string]any{
		"name": "shared", "storage_source_id": src.ID, "path": "/data",
		"access_key_id": "ak", "secret_access_key": "sk",
	})
	dsID := itoa(int(ds["id"].(float64)))

	// Find the edit user id
	var ed database.User
	require.NoError(t, d.DB.Where("username = ?", "ed").First(&ed).Error)

	// Create binding (admin-level required; admin OK)
	w := doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/bindings", admin,
		map[string]any{"user_id": ed.ID, "permission": "read"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var b map[string]any
	require.NoError(t, unmarshalBody(w, &b))
	bindingID := itoa(int(b["id"].(float64)))
	assert.EqualValues(t, ed.ID, b["user_id"])
	assert.Equal(t, "read", b["permission"])
	assert.NotNil(t, b["created_by_user_id"])

	// Unknown user → 400
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/bindings", admin,
		map[string]any{"user_id": 999, "permission": "read"})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Duplicate → 409
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/bindings", admin,
		map[string]any{"user_id": ed.ID, "permission": "write"})
	assert.Equal(t, http.StatusConflict, w.Code)

	// Invalid permission → 422
	w = doJSON(r, http.MethodPost, "/api/data-sources/"+dsID+"/bindings", admin,
		map[string]any{"user_id": 1, "permission": "superuser"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// After read binding, edit can see the DS in the list + get it
	editTok := mkEditToken(t, d, r)
	w = doJSON(r, http.MethodGet, "/api/data-sources", editTok, nil)
	var rows []map[string]any
	require.NoError(t, unmarshalBody(w, &rows))
	assert.Len(t, rows, 1)
	w = doJSON(r, http.MethodGet, "/api/data-sources/"+dsID, editTok, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	// read binding cannot write
	w = doJSON(r, http.MethodPut, "/api/data-sources/"+dsID, editTok,
		map[string]any{"description": "nope"})
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Upgrade binding to write
	w = doJSON(r, http.MethodPut, "/api/data-sources/"+dsID+"/bindings/"+bindingID, admin,
		map[string]any{"permission": "write"})
	require.Equal(t, http.StatusOK, w.Code)
	w = doJSON(r, http.MethodPut, "/api/data-sources/"+dsID, editTok,
		map[string]any{"description": "now can"})
	assert.Equal(t, http.StatusOK, w.Code)

	// 404 when binding belongs to another data source
	other := createDS(t, r, admin, map[string]any{
		"name": "other", "storage_source_id": src.ID, "path": "/o",
		"access_key_id": "a", "secret_access_key": "b",
	})
	otherID := itoa(int(other["id"].(float64)))
	w = doJSON(r, http.MethodDelete, "/api/data-sources/"+otherID+"/bindings/"+bindingID, admin, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Delete binding → edit loses access
	w = doJSON(r, http.MethodDelete, "/api/data-sources/"+dsID+"/bindings/"+bindingID, admin, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = doJSON(r, http.MethodGet, "/api/data-sources/"+dsID, editTok, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}
