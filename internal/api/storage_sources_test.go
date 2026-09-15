package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/database"
)

// valid s3 create body (provider now lives in extra and is required).
func s3Body(name string) map[string]any {
	return map[string]any{
		"name": name, "type": "s3", "endpoint": "http://10.0.0.1:9000",
		"extra": map[string]any{"provider": "Minio"},
	}
}

func TestStorageSourceCRUD(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	mkUser(t, d, "ed", "edit")
	adminTok := login(t, r, "root", "pass1234")
	editTok := login(t, r, "ed", "pass1234")

	// Create (admin)
	w := doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, s3Body("minio-main"))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var src map[string]any
	require.NoError(t, unmarshalBody(w, &src))
	assert.Equal(t, "minio-main", src["name"])
	assert.EqualValues(t, "Minio", src["extra"].(map[string]any)["provider"])
	srcID := int(src["id"].(float64))

	// Duplicate name → 409 (body must pass validation first)
	w = doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, s3Body("minio-main"))
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already exists")

	// Bad name pattern → 422
	w = doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, s3Body("bad name!"))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// view role cannot create; edit role cannot either
	mkUser(t, d, "viewer", "view")
	viewTok := login(t, r, "viewer", "pass1234")
	w = doJSON(r, http.MethodPost, "/api/storage-sources", viewTok, s3Body("x1"))
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = doJSON(r, http.MethodPost, "/api/storage-sources", editTok, s3Body("x2"))
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Get / list (any role)
	w = doJSON(r, http.MethodGet, "/api/storage-sources/"+itoa(srcID), viewTok, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	w = doJSON(r, http.MethodGet, "/api/storage-sources", viewTok, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	// 404
	w = doJSON(r, http.MethodGet, "/api/storage-sources/999", adminTok, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "storage source not found")

	// Partial update
	w = doJSON(r, http.MethodPut, "/api/storage-sources/"+itoa(srcID), adminTok,
		map[string]any{"region": "us-east-1"})
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, unmarshalBody(w, &src))
	assert.Equal(t, "us-east-1", src["region"])
	assert.Equal(t, "minio-main", src["name"], "unset fields untouched")

	// Delete-in-use → 409 (create a data source first)
	require.NoError(t, d.DB.Create(&database.DataSource{
		Name: "d1", StorageSourceID: int64(srcID), Path: "/data", OwnerUserID: 1,
	}).Error)
	w = doJSON(r, http.MethodDelete, "/api/storage-sources/"+itoa(srcID), adminTok, nil)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "used by a data source")

	// Delete unused source → 204
	w = doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, s3Body("unused"))
	require.Equal(t, http.StatusCreated, w.Code)
	var unused map[string]any
	require.NoError(t, unmarshalBody(w, &unused))
	w = doJSON(r, http.MethodDelete, "/api/storage-sources/"+itoa(int(unused["id"].(float64))), adminTok, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestStorageSourceTypeConstraints(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	tok := login(t, r, "root", "pass1234")

	// s3 without extra.provider → 422
	w := doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "s3-noprovider", "type": "s3", "endpoint": "http://x:9000",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "provider is required for s3")

	// s3 with path → 422 (path is local-only)
	w = doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "s3-withpath", "type": "s3", "path": "/mnt/data",
		"extra": map[string]any{"provider": "Minio"},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "path is for the local backend")

	// local with endpoint → 422
	w = doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "local-bad", "type": "local", "endpoint": "http://x", "path": "/mnt",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "endpoint is for network backends")

	// local with region → 422
	w = doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "local-bad2", "type": "local", "region": "us-east-1", "path": "/mnt",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// unknown type → 422
	w = doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "cos-legacy", "type": "cos",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "must be one of: s3, local")

	// local with path → 201, provider not required
	w = doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "fs-main", "type": "local", "path": "/mnt/rclone-roots",
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "local", out["type"])
	assert.Equal(t, "/mnt/rclone-roots", out["path"])
	assert.Nil(t, out["endpoint"])
}

func TestLocalPathFlowsToRcloneRemote(t *testing.T) {
	// The remote parameters builder must use extra.root? No — storage_source
	// path is the FS prefix and lands in extra.root so ds-N:/abs resolves
	// under it (see services.BuildRemoteParameters local branch).
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	tok := login(t, r, "root", "pass1234")

	w := doJSON(r, http.MethodPost, "/api/storage-sources", tok, map[string]any{
		"name": "fs", "type": "local", "path": "/srv/roots",
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}
