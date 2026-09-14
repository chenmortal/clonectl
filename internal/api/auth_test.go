package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginSuccess(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "admin", "admin")

	w := doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "admin", "password": "pass1234",
	})
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "bearer", out["token_type"])
	assert.EqualValues(t, 480*60, out["expires_in"])
	assert.Equal(t, "admin", out["role"])
	assert.NotEmpty(t, out["access_token"])

	// last_login_at updated
	var u struct {
		LastLoginAt *string `json:"last_login_at"`
	}
	me := doJSON(r, http.MethodGet, "/api/auth/me", out["access_token"].(string), nil)
	require.Equal(t, http.StatusOK, me.Code)
	require.NoError(t, unmarshalBody(me, &u))
	assert.NotNil(t, u.LastLoginAt)
}

// All failure modes collapse to the same 401 body (no account-existence leak).
func TestLoginFailuresUniform(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "alice", "view")

	for _, tc := range []struct {
		name     string
		username string
	}{
		{"wrong password", "alice"},
		{"unknown user", "nobody"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
				"username": tc.username, "password": "wrong-password",
			})
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.JSONEq(t, `{"detail":{"error":"invalid_credentials"}}`, w.Body.String())
			assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"))
		})
	}

	// Disabled account
	w := doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "alice", "password": "pass1234",
	})
	require.Equal(t, http.StatusOK, w.Code, "alice is not disabled yet")
}

func TestDisabledUserCannotLogin(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "alice", "view")
	mkUser(t, d, "root", "admin")
	token := login(t, r, "root", "pass1234")

	// Disable alice via PUT
	w := doJSON(r, http.MethodGet, "/api/users", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var rows []map[string]any
	require.NoError(t, unmarshalBody(w, &rows))
	var aliceID int
	for _, row := range rows {
		if row["username"] == "alice" {
			aliceID = int(row["id"].(float64))
		}
	}
	require.NotZero(t, aliceID)
	w = doJSON(r, http.MethodPut, "/api/users/"+itoa(aliceID), token, map[string]any{"disabled": true})
	require.Equal(t, http.StatusOK, w.Code)

	// Login now fails with the uniform 401.
	w = doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "alice", "password": "pass1234",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.JSONEq(t, `{"detail":{"error":"invalid_credentials"}}`, w.Body.String())
}

func TestMeRequiresToken(t *testing.T) {
	_, r := newTestEnv(t)

	w := doJSON(r, http.MethodGet, "/api/auth/me", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "unauthenticated")
	assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"))

	w = doJSON(r, http.MethodGet, "/api/auth/me", "garbage-token", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "invalid token")
}

func TestChangePassword(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "alice", "edit")
	token := login(t, r, "alice", "pass1234")

	// wrong old password
	w := doJSON(r, http.MethodPost, "/api/auth/change-password", token, map[string]string{
		"old_password": "nope", "new_password": "newpassword1",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "old_password mismatch")

	// short new password
	w = doJSON(r, http.MethodPost, "/api/auth/change-password", token, map[string]string{
		"old_password": "pass1234", "new_password": "short",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// happy path
	w = doJSON(r, http.MethodPost, "/api/auth/change-password", token, map[string]string{
		"old_password": "pass1234", "new_password": "newpassword1",
	})
	assert.Equal(t, http.StatusNoContent, w.Code)

	// old password no longer works; new one does
	w = doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "alice", "password": "pass1234",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "alice", "password": "newpassword1",
	})
	assert.Equal(t, http.StatusOK, w.Code)
}
