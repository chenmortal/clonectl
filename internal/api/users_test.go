package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RBAC matrix for /api/users (admin-only) + auth-less 401s.
func TestUsersRBAC(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	mkUser(t, d, "ed", "edit")
	mkUser(t, d, "viewer", "view")

	adminTok := login(t, r, "root", "pass1234")
	editTok := login(t, r, "ed", "pass1234")
	viewTok := login(t, r, "viewer", "pass1234")

	// No token → 401 on every real route (auth runs before role check).
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodPut, "/api/users/1"},
		{http.MethodDelete, "/api/users/1"},
		{http.MethodPost, "/api/users/1/reset-password"},
	} {
		w := doJSON(r, tc.method, tc.path, "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code, tc.method+" "+tc.path)
	}

	// view role → 403 with sorted required + actual
	w := doJSON(r, http.MethodGet, "/api/users", viewTok, nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"detail":{"error":"forbidden","required":["admin"],"actual":"view"}}`, w.Body.String())

	w = doJSON(r, http.MethodGet, "/api/users", editTok, nil)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// admin → ok
	w = doJSON(r, http.MethodGet, "/api/users", adminTok, nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUsersCRUD(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	token := login(t, r, "root", "pass1234")

	// Create
	w := doJSON(r, http.MethodPost, "/api/users", token, map[string]string{
		"username": "bob", "password": "bobpass12", "role": "edit",
	})
	require.Equal(t, http.StatusCreated, w.Code)
	var created map[string]any
	require.NoError(t, unmarshalBody(w, &created))
	assert.Equal(t, "bob", created["username"])
	assert.Equal(t, "edit", created["role"])
	bobID := int(created["id"].(float64))

	// Duplicate username → 409
	w = doJSON(r, http.MethodPost, "/api/users", token, map[string]string{
		"username": "bob", "password": "bobpass12", "role": "view",
	})
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "username_taken")

	// Invalid role → 400
	w = doJSON(r, http.MethodPost, "/api/users", token, map[string]string{
		"username": "carol", "password": "carolpass1", "role": "superuser",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_role")

	// Short password → 422
	w = doJSON(r, http.MethodPost, "/api/users", token, map[string]string{
		"username": "carol", "password": "short", "role": "view",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// Update: disable + change role
	w = doJSON(r, http.MethodPut, fmt.Sprintf("/api/users/%d", bobID), token,
		map[string]any{"role": "view", "disabled": true})
	require.Equal(t, http.StatusOK, w.Code)
	var upd map[string]any
	require.NoError(t, unmarshalBody(w, &upd))
	assert.Equal(t, "view", upd["role"])
	assert.NotNil(t, upd["disabled_at"])

	// 404 on missing user
	w = doJSON(r, http.MethodPut, "/api/users/999", token, map[string]any{"disabled": false})
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Reset password
	w = doJSON(r, http.MethodPost, fmt.Sprintf("/api/users/%d/reset-password", bobID), token,
		map[string]string{"new_password": "newbobpass1"})
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Delete self → 400
	w = doJSON(r, http.MethodGet, "/api/users", token, nil)
	var rows []map[string]any
	require.NoError(t, unmarshalBody(w, &rows))
	var rootID int
	for _, row := range rows {
		if row["username"] == "root" {
			rootID = int(row["id"].(float64))
		}
	}
	w = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/users/%d", rootID), token, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "cannot_delete_self")

	// Delete bob → 204; then 404
	w = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/users/%d", bobID), token, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = doJSON(r, http.MethodDelete, fmt.Sprintf("/api/users/%d", bobID), token, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
