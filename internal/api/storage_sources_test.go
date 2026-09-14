package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/database"
)

func TestStorageSourceCRUD(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	mkUser(t, d, "ed", "edit")
	adminTok := login(t, r, "root", "pass1234")
	editTok := login(t, r, "ed", "pass1234")

	// Create (admin)
	w := doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, map[string]any{
		"name": "minio-main", "type": "s3", "endpoint": "http://10.0.0.1:9000",
		"extra": map[string]any{"provider": "Minio"},
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var src map[string]any
	require.NoError(t, unmarshalBody(w, &src))
	assert.Equal(t, "minio-main", src["name"])
	assert.EqualValues(t, "Minio", src["extra"].(map[string]any)["provider"])
	srcID := int(src["id"].(float64))

	// Duplicate name → 409
	w = doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, map[string]any{
		"name": "minio-main", "type": "s3",
	})
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already exists")

	// Bad name pattern → 422
	w = doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, map[string]any{
		"name": "bad name!", "type": "s3",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// view role cannot create; edit role cannot either
	mkUser(t, d, "viewer", "view")
	viewTok := login(t, r, "viewer", "pass1234")
	w = doJSON(r, http.MethodPost, "/api/storage-sources", viewTok, map[string]any{
		"name": "x1", "type": "s3",
	})
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = doJSON(r, http.MethodPost, "/api/storage-sources", editTok, map[string]any{
		"name": "x2", "type": "s3",
	})
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
	w = doJSON(r, http.MethodPost, "/api/storage-sources", adminTok, map[string]any{
		"name": "unused", "type": "s3",
	})
	require.Equal(t, http.StatusCreated, w.Code)
	var unused map[string]any
	require.NoError(t, unmarshalBody(w, &unused))
	w = doJSON(r, http.MethodDelete, "/api/storage-sources/"+itoa(int(unused["id"].(float64))), adminTok, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}
