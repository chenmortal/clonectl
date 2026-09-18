package rclone

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"clonectl/internal/rclone/rctest"
)

func newTestClient(t *testing.T, srv *rctest.Server) *Client {
	t.Helper()
	return NewClient(srv.URL(), "u", "p", 5*time.Second)
}

func TestStartSyncReturnsJobid(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	id, err := c.StartSync("src:/a", "dst:/b", "sync", map[string]any{"transfers": float64(4)})
	require.NoError(t, err)
	assert.EqualValues(t, 1, id)

	rec := srv.Records()
	require.Len(t, rec, 1)
	assert.Equal(t, "/sync/sync", rec[0].Path)
	assert.Equal(t, "src:/a", rec[0].Body["srcFs"])
	assert.Equal(t, "dst:/b", rec[0].Body["dstFs"])
	assert.Equal(t, true, rec[0].Body["_async"])
	cfg, ok := rec[0].Body["_config"].(map[string]any)
	require.True(t, ok, "_config must carry options")
	// 规范名 transfers 翻译成 rc 线上字段名 Transfers(fs.ConfigInfo 无 json 标签)。
	assert.EqualValues(t, 4, cfg["Transfers"])
}

func TestStartSyncSplitsFilterOptions(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	options := map[string]any{
		"transfers":   float64(8),
		"dry_run":     true,
		"min_size":    "1M",
		"max_age":     "24h",
		"what_is_this": true, // 未知键:原样进 _config,由 rclone 决定去留
	}
	_, err := c.StartSync("a", "b", "copy", options)
	require.NoError(t, err)

	rec := srv.Records()[0]
	cfg, ok := rec.Body["_config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Transfers", keyOf(cfg, float64(8)))
	assert.Equal(t, "DryRun", keyOf(cfg, true))
	assert.Equal(t, "what_is_this", keyOf(cfg, true, "DryRun"))

	flt, ok := rec.Body["_filter"].(map[string]any)
	require.True(t, ok, "filter keys must ride in _filter")
	assert.Equal(t, "MinSize", keyOf(flt, "1M"))
	assert.Equal(t, "MaxAge", keyOf(flt, "24h"))
}

// keyOf finds the map key holding want (skipping skip keys); fails via t if absent.
func keyOf(m map[string]any, want any, skip ...string) string {
	for k, v := range m {
		if !slices.Contains(skip, k) && assert.ObjectsAreEqual(want, v) {
			return k
		}
	}
	return "<missing>"
}

func TestStartSyncCopyMode(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	id, err := c.StartSync("a", "b", "copy", nil)
	require.NoError(t, err)
	assert.EqualValues(t, 1, id)
	assert.Equal(t, "/sync/copy", srv.Records()[0].Path)
	// No options → no _config key.
	_, has := srv.Records()[0].Body["_config"]
	assert.False(t, has)
}

func TestStartSyncHTTPErrorRaises(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	srv.FailNext("/sync/sync", 500, map[string]any{"error": "boom"})
	c := newTestClient(t, srv)

	_, err := c.StartSync("a", "b", "sync", nil)
	require.Error(t, err)
	var apiErr *APIError
	assert.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 500, apiErr.StatusCode)
	assert.Equal(t, "boom", apiErr.Body["error"])
	assert.Contains(t, err.Error(), "returned 500")
}

func TestCreateAndDeleteRemotePayloads(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.CreateRemote("r1", "s3", map[string]any{"provider": "Minio"})
	require.NoError(t, err)
	// DeleteRemote was removed: legacy /api/storages write path is 410 Gone.
	// Verify the underlying RC API still answers /config/delete directly,
	// which is the shape any future wrapper would use.
	httpReq, _ := http.NewRequest(http.MethodPost, srv.URL()+"/config/delete",
		strings.NewReader(`{"name":"r1"}`))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, derr := http.DefaultClient.Do(httpReq)
	require.NoError(t, derr)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	rec := srv.Records()
	require.Len(t, rec, 2)
	assert.Equal(t, "/config/create", rec[0].Path)
	assert.Equal(t, "r1", rec[0].Body["name"])
	assert.Equal(t, "s3", rec[0].Body["type"])
	assert.Equal(t, "/config/delete", rec[1].Path)
	assert.Equal(t, "r1", rec[1].Body["name"])
}

func TestJobStatusFailedJobDoesNotRaise(t *testing.T) {
	// A finished job returns 200 with body.error — allow_error_field parity.
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	srv.QueueJobStatus(7, rctest.FinishedError(7, "sync failed: no such bucket"))
	st, err := c.JobStatus(7)
	require.NoError(t, err)
	assert.Equal(t, true, st["finished"])
	assert.Equal(t, false, st["success"])
	assert.Equal(t, "sync failed: no such bucket", st["error"])
}

func TestJobStatusErrorFieldRaisesWhenDisallowed(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	// start_sync response body carrying error → error (default strictness).
	srv.FailNext("/sync/sync", 200, map[string]any{"error": "denied"})
	c := newTestClient(t, srv)

	_, err := c.StartSync("a", "b", "sync", nil)
	require.Error(t, err)
	assert.Equal(t, "denied", err.Error())
}

func TestJobStatusAndStats(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	srv.SetJobStats(3, map[string]any{"bytes": float64(123), "transfers": float64(2)})
	st, err := c.JobStatus(3)
	require.NoError(t, err)
	assert.Equal(t, true, st["finished"])
	stats, err := c.JobStats(3)
	require.NoError(t, err)
	assert.EqualValues(t, 123, stats["bytes"])

	assert.Equal(t, "/job/status", srv.Records()[0].Path)
	assert.Equal(t, "job/3", srv.Records()[1].Body["group"])
}

func TestPingTrueAndFalse(t *testing.T) {
	srv := rctest.New()
	c := newTestClient(t, srv)
	assert.True(t, c.Ping())

	srv.SetVersionOK(false)
	assert.False(t, c.Ping())
	srv.Close()

	// Server down → false, never panics.
	assert.False(t, c.Ping())
}

func TestStartSyncMissingJobid(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	srv.FailNext("/sync/sync", 200, map[string]any{"nope": true})
	c := newTestClient(t, srv)

	_, err := c.StartSync("a", "b", "sync", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no jobid")
}

func TestStartCheckMergesOptionsTopLevel(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	id, err := c.StartCheck("a", "b", map[string]any{"oneWay": true, "combined": "/tmp/x"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, id)

	body := srv.Records()[0].Body
	assert.Equal(t, "/operations/check", srv.Records()[0].Path)
	assert.Equal(t, true, body["oneWay"])
	assert.Equal(t, "/tmp/x", body["combined"])
	assert.Equal(t, true, body["_async"])
	_, has := body["_config"]
	assert.False(t, has, "check options merge top-level, not under _config")
}

func TestListNormalizesShapes(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	srv.SetListResult([]map[string]any{{"Name": "a.txt"}, {"Name": "b.txt"}})
	entries, err := c.List("ds-1:/data")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "a.txt", entries[0]["Name"])
	assert.Equal(t, "", srv.Records()[0].Body["remote"])

	// Server error surfaces.
	srv.FailListNext(500, "nope")
	_, err = c.List("ds-1:/data")
	require.Error(t, err)
}

func TestWriteProbeSuccessAndFailures(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()
	c := newTestClient(t, srv)

	ok, warn := c.WriteProbe("ds-1:/data")
	assert.True(t, ok)
	assert.Empty(t, warn)
	rec := srv.Records()
	require.Len(t, rec, 2)
	assert.Equal(t, "/operations/copyfile", rec[0].Path)
	assert.Equal(t, "ds-1:/data", rec[0].Body["dstFs"])
	assert.Equal(t, "/operations/deletefile", rec[1].Path)

	// Upload failure → (false, err message is the HTTP-level message,
	// matching Python's str(RcloneApiError))
	srv2 := rctest.New()
	defer srv2.Close()
	srv2.FailCopyNext(500, "denied")
	ok, warn = newTestClient(t, srv2).WriteProbe("ds-1:/data")
	assert.False(t, ok)
	assert.Contains(t, warn, "returned 500")

	// Cleanup failure → (true, warning)
	srv3 := rctest.New()
	defer srv3.Close()
	srv3.FailDeleteNext(500, "readonly")
	ok, warn = newTestClient(t, srv3).WriteProbe("ds-1:/data")
	assert.True(t, ok)
	assert.Contains(t, warn, "delete failed")
}

func TestTransportErrorWrapped(t *testing.T) {
	srv := rctest.New()
	url := srv.URL()
	srv.Close()
	c := NewClient(url, "u", "p", time.Second)

	_, err := c.CreateRemote("x", "s3", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("request /config/create failed"))
}
