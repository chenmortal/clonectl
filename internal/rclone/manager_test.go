package rclone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/rclone/rctest"
)

func TestRCNormalMapsWildcardHosts(t *testing.T) {
	assert.Equal(t, "127.0.0.1:5572", RCNormal("http://0.0.0.0:5572"))
	assert.Equal(t, "127.0.0.1:5572", RCNormal("http://[::]:5572"))
	assert.Equal(t, "localhost:5572", RCNormal("http://localhost:5572"))
}

func TestStartAlreadyRunningSkips(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()

	// Point the manager at the running fake rcd (no binary needed).
	m := NewManager(srv.URL(), srv.URL(), "u", "p", "/nonexistent/rclone")
	require.NoError(t, m.Start(2*time.Second))
	assert.Nil(t, m.cmd, "no process spawned")
}

func TestStartNotReadyTimesOut(t *testing.T) {
	srv := rctest.New()
	url := srv.URL()
	srv.Close() // nothing listening

	m := NewManager(url, url, "u", "p", "/nonexistent/rclone")
	err := m.Start(1 * time.Second)
	require.Error(t, err)
	// Either the binary fails to exec or readiness times out — both mean
	// "rcd not usable" and the manager reports it.
	assert.True(t,
		strings.Contains(err.Error(), "not ready within") ||
			strings.Contains(err.Error(), "exited early") ||
			strings.Contains(err.Error(), "start rclone rcd"),
		err.Error())
}

func TestReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pid")

	assert.Equal(t, 0, ReadPIDFile(path), "missing file → 0")

	require.NoError(t, os.WriteFile(path, []byte("4242\n"), 0o644))
	assert.Equal(t, 4242, ReadPIDFile(path))

	require.NoError(t, os.WriteFile(path, []byte("garbage"), 0o644))
	assert.Equal(t, 0, ReadPIDFile(path), "garbage → 0")
}
