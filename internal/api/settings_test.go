package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemSettingsCRUD(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	token := login(t, r, "root", "pass1234")

	// 404 on missing
	w := doJSON(r, http.MethodGet, "/api/system-settings/nope", token, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "setting not found")

	// Upsert
	w = doJSON(r, http.MethodPut, "/api/system-settings/mykey", token,
		map[string]string{"value": "v1"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var row map[string]any
	require.NoError(t, unmarshalBody(w, &row))
	assert.Equal(t, "mykey", row["key"])
	assert.Equal(t, "v1", row["value"])
	assert.NotNil(t, row["updated_by_user_id"])

	// Get + list
	w = doJSON(r, http.MethodGet, "/api/system-settings/mykey", token, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	w = doJSON(r, http.MethodGet, "/api/system-settings", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var rows []map[string]any
	require.NoError(t, unmarshalBody(w, &rows))
	assert.Len(t, rows, 1)

	// Overwrite
	w = doJSON(r, http.MethodPut, "/api/system-settings/mykey", token,
		map[string]string{"value": "v2"})
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, unmarshalBody(w, &row))
	assert.Equal(t, "v2", row["value"])

	// Non-admin denied
	mkUser(t, d, "viewer", "view")
	w = doJSON(r, http.MethodGet, "/api/system-settings", login(t, r, "viewer", "pass1234"), nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAlertmanagerURLValidation(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	token := login(t, r, "root", "pass1234")

	// Invalid scheme → 422
	w := doJSON(r, http.MethodPut, "/api/system-settings/alertmanager_url", token,
		map[string]string{"value": "ftp://x"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "scheme must be http or https")

	// Garbage → 422
	w = doJSON(r, http.MethodPut, "/api/system-settings/alertmanager_url", token,
		map[string]string{"value": "://bad url"})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// Valid http → OK
	w = doJSON(r, http.MethodPut, "/api/system-settings/alertmanager_url", token,
		map[string]string{"value": "http://am:9093/api/v2/alerts"})
	assert.Equal(t, http.StatusOK, w.Code)

	// Empty value allowed (disables)
	w = doJSON(r, http.MethodPut, "/api/system-settings/alertmanager_url", token,
		map[string]string{"value": ""})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAlertmanagerTestEndpoint(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")
	token := login(t, r, "root", "pass1234")

	// Not configured → 400
	w := doJSON(r, http.MethodPost, "/api/system-settings/internal/alertmanager-test", token,
		map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not configured")

	// With a receiver
	var got map[string]any
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		got = body
		w.WriteHeader(200)
	}))
	defer am.Close()

	w = doJSON(r, http.MethodPut, "/api/system-settings/alertmanager_url", token,
		map[string]string{"value": am.URL})
	require.Equal(t, http.StatusOK, w.Code)

	w = doJSON(r, http.MethodPost, "/api/system-settings/internal/alertmanager-test", token,
		map[string]any{"alertname": "ManualTest", "labels": map[string]string{"env": "test"}})
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, true, out["sent"])
	assert.Nil(t, out["error"])

	require.NotNil(t, got)
	assert.Equal(t, "4", got["version"])
	assert.Equal(t, "firing", got["status"])
	// Synthetic test payload: task 0 / run 0. Python's groupKey format
	// braces the kind: "{run.sync}.0".
	assert.Equal(t, "{run.sync}.0", got["groupKey"])
	alerts := got["alerts"].([]any)
	first := alerts[0].(map[string]any)
	assert.Equal(t, "0001-01-01T00:00:00Z", first["endsAt"], "firing sentinel")
	labels := first["labels"].(map[string]any)
	assert.Equal(t, "ManualTest", labels["alertname"])
	assert.Equal(t, "test", labels["env"])
	assert.Equal(t, "critical", labels["severity"])
}

func TestSiteInfo(t *testing.T) {
	d, r := newTestEnv(t)
	mkUser(t, d, "root", "admin")

	// Public and empty before configuration
	w := doJSON(r, http.MethodGet, "/api/site-info", "", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "", out["site_title"])

	// Admin sets the title; value is trimmed
	token := login(t, r, "root", "pass1234")
	w = doJSON(r, http.MethodPut, "/api/system-settings/site_title", token,
		map[string]string{"value": "  我的同步台  "})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Still public for anonymous viewers (login page needs it)
	w = doJSON(r, http.MethodGet, "/api/site-info", "", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, unmarshalBody(w, &out))
	assert.Equal(t, "我的同步台", out["site_title"])

	// Over 100 runes → 422
	w = doJSON(r, http.MethodPut, "/api/system-settings/site_title", token,
		map[string]string{"value": strings.Repeat("标", 101)})
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	// Exactly 100 runes → OK
	w = doJSON(r, http.MethodPut, "/api/system-settings/site_title", token,
		map[string]string{"value": strings.Repeat("标", 100)})
	assert.Equal(t, http.StatusOK, w.Code)
}
