package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/auth"
	"rclone_sync/internal/config"
	"rclone_sync/internal/database"
)

// newTestEnv builds an isolated DB + router. Bcrypt cost is minimized for speed.
func newTestEnv(t *testing.T) (*Deps, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	d := &Deps{DB: db, Cfg: config.Settings{
		JWTSecret:     "test-secret",
		JWTAlgorithm:  "HS256",
		JWTExpiresMin: 480,
		BcryptRounds:  4,
	}}
	return d, NewRouter(d)
}

func mkUser(t *testing.T, d *Deps, username, role string) *database.User {
	t.Helper()
	hash, err := auth.HashPassword("pass1234", 4)
	require.NoError(t, err)
	u := database.User{Username: username, PasswordHash: hash, Role: role}
	require.NoError(t, d.DB.Create(&u).Error)
	return &u
}

// login posts credentials and returns the access token.
func login(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	w := doJSON(r, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": username, "password": password,
	})
	require.Equal(t, http.StatusOK, w.Code, "login body: %s", w.Body.String())
	var out struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out.AccessToken
}

// doJSON issues a request; empty token means unauthenticated.
func doJSON(r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	if body == nil {
		body = map[string]any{}
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	httpReq, err := http.NewRequest(method, path, bytes.NewReader(b))
	if err != nil {
		panic(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpReq)
	return w
}

func unmarshalBody(w *httptest.ResponseRecorder, v any) error {
	return json.Unmarshal(w.Body.Bytes(), v)
}

func itoa(n int) string { return strconv.Itoa(n) }
