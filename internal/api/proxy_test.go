package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/rclone"
	"rclone_sync/internal/rclone/rctest"
)

func proxyEnv(t *testing.T) (*Deps, *gin.Engine, string, *rctest.Server) {
	t.Helper()
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	mkUser(t, d, "viewer", "view")
	admin := login(t, r, "root", "pass1234")

	srv := rctest.New()
	t.Cleanup(srv.Close)
	d.RC = rclone.NewClient(srv.URL(), "rcu", "rcp", 0)

	upstream, err := url.Parse(srv.URL())
	require.NoError(t, err)
	d.RCProxy = NewRcloneProxy(upstream, "rcu", "rcp")
	// Router already wires /healthz; rebuild to pick up the proxy routes.
	r = NewRouter(d)
	return d, r, admin, srv
}

func TestProxyForwardsPathBody(t *testing.T) {
	_, r, admin, srv := proxyEnv(t)

	req, _ := http.NewRequest(http.MethodPost, "/rclone/core/version", strings.NewReader(`{"jobid":3}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	req.Header.Set("X-Custom", "keep-me")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	recs := srv.Records()
	require.Len(t, recs, 1)
	assert.Equal(t, "/core/version", recs[0].Path, "prefix stripped, path forwarded")
	assert.EqualValues(t, 3, recs[0].Body["jobid"], "body forwarded")
}

func TestProxyMethodRoles(t *testing.T) {
	_, r, admin, _ := proxyEnv(t)
	view := login(t, r, "viewer", "pass1234")

	// GET allowed for any role
	w := doJSON(r, http.MethodGet, "/rclone/core/version", view, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	// POST for viewer → 403
	w = doJSON(r, http.MethodPost, "/rclone/core/version", view, map[string]any{})
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "forbidden")

	// POST admin → passthrough
	w = doJSON(r, http.MethodPost, "/rclone/core/version", admin, map[string]any{})
	assert.Equal(t, http.StatusOK, w.Code)

	// No token → 401
	w = doJSON(r, http.MethodPost, "/rclone/core/version", "", map[string]any{})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProxyUpstreamError502(t *testing.T) {
	d, _, admin, srv := proxyEnv(t)
	deadURL := srv.URL()
	srv.Close() // upstream now down

	// Re-point the proxy at the dead server and rebuild the router.
	upstream, err := url.Parse(deadURL)
	require.NoError(t, err)
	d.RCProxy = NewRcloneProxy(upstream, "rcu", "rcp")
	r2 := NewRouter(d)

	req, _ := http.NewRequest(http.MethodGet, "/rclone/core/version", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "rclone rcd unreachable")
}

func TestHealthz(t *testing.T) {
	// /healthz is wired by NewRouter already.
	_, r := newTestEnv(t)

	w := doJSON(r, http.MethodGet, "/healthz", "", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "ok", out["status"])
	assert.Equal(t, false, out["rclone_reachable"], "nil RC → false")
	assert.Equal(t, false, out["is_leader"])
	assert.Equal(t, "", out["node_id"])
	assert.Nil(t, out["leader_id"])
	assert.NotNil(t, out["peers"])
}

func TestSPAStaticServing(t *testing.T) {
	d, r := newTestEnv(t)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "assets"), 0o755))
	write := func(rel, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644))
	}
	write("index.html", "<html>spa</html>")
	write("favicon.ico", "ico")
	write("assets/app.js", "console.log(1)")
	d.Static = DiskStaticFS(root)
	MountStatic(r, d.Static)

	// / serves index
	w := doJSON(r, http.MethodGet, "/", "", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "spa")

	// real file served
	w = doJSON(r, http.MethodGet, "/favicon.ico", "", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ico", w.Body.String())

	// assets get immutable cache header
	w = doJSON(r, http.MethodGet, "/assets/app.js", "", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))

	// unknown path → SPA fallback (history mode routing)
	w = doJSON(r, http.MethodGet, "/tasks/3", "", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "spa")

	// unknown /api path → JSON 404 (documented deviation from Python)
	w = doJSON(r, http.MethodGet, "/api/nonexistent", "", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "Not Found")

	// path escape → index.html (no traversal)
	w = doJSON(r, http.MethodGet, "/..%2f..%2fetc/passwd", "", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "spa")
}
