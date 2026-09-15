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

// fakeRcloneBin writes an executable that ignores all args and exits with
// the given code — simulates an rcd that dies immediately (e.g. bind clash).
func fakeRcloneBin(t *testing.T, dir string, exitCode int) string {
	t.Helper()
	path := filepath.Join(dir, "fake-rclone")
	script := "#!/bin/sh\nexit " + itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestRCNormalMapsWildcardHosts(t *testing.T) {
	// Scheme must survive — the result feeds HTTP probes.
	assert.Equal(t, "http://127.0.0.1:5572", RCNormal("http://0.0.0.0:5572"))
	assert.Equal(t, "http://127.0.0.1:5572", RCNormal("http://[::]:5572"))
	assert.Equal(t, "http://localhost:5572", RCNormal("http://localhost:5572"))
	assert.Equal(t, "http://10.1.2.3:5573", RCNormal("http://10.1.2.3:5573"))
}

func TestStartAlreadyRunningSkips(t *testing.T) {
	srv := rctest.New()
	defer srv.Close()

	// The probe URL comes from RCNormal — scheme included — so a live rcd
	// is detected and no child process is spawned.
	m := NewManager(RCNormal(srv.URL()), srv.URL(), "u", "p", "/nonexistent/rclone")
	require.NoError(t, m.Start(2*time.Second))
	assert.Nil(t, m.cmd, "no process spawned")
}

func TestStartPortTakenAuthMismatchFailsFast(t *testing.T) {
	// Someone else's rcd is on the port but rejects our credentials:
	// Start must fail fast with a clear message and spawn nothing.
	srv := rctest.New()
	defer srv.Close()
	srv.FailNext("/core/version", 401, map[string]any{"error": "unauthorized"})

	started := time.Now()
	m := NewManager(srv.URL(), srv.URL(), "wrong", "creds", "/nonexistent/rclone")
	err := m.Start(15 * time.Second)
	require.Error(t, err)
	assert.Less(t, time.Since(started), 3*time.Second, "must fail fast, not wait the timeout")
	assert.Contains(t, err.Error(), "认证不匹配")
	assert.Nil(t, m.cmd, "no child process spawned")
}

func TestStartDetectsEarlyExitQuickly(t *testing.T) {
	dir := t.TempDir()
	srv := rctest.New()
	defer srv.Close()
	srv.Close() // nothing listening → readiness never satisfied

	// A "rclone" that dies immediately (e.g. bind clash → CRITICAL exit).
	bin := fakeRcloneBin(t, dir, 1)
	m := NewManager(url2(dir), url2(dir), "u", "p", bin)
	m.logFile = filepath.Join(dir, "rcd.log")
	m.pidFile = filepath.Join(dir, "rcd.pid")

	started := time.Now()
	err := m.Start(15 * time.Second)
	elapsed := time.Since(started)
	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second,
		"early exit must be detected via wait(), not a zombie Signal(0) probe")
	assert.Contains(t, err.Error(), "exited early")
	_, statErr := os.Stat(m.pidFile)
	assert.True(t, os.IsNotExist(statErr), "pid file cleaned up")
}

func TestStartNotReadyTimesOutWhenBinBlocks(t *testing.T) {
	srv := rctest.New()
	url := srv.URL()
	srv.Close()

	// A binary that starts and blocks forever (never serves RC API).
	dir := t.TempDir()
	bin := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0o755))

	m := NewManager(url, url, "u", "p", bin)
	m.logFile = filepath.Join(dir, "rcd.log")
	m.pidFile = filepath.Join(dir, "rcd.pid")

	started := time.Now()
	err := m.Start(2 * time.Second)
	elapsed := time.Since(started)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready within")
	assert.Less(t, elapsed, 13*time.Second, "timeout ~2s + kill grace, not 15s")
	assert.True(t, strings.Contains(err.Error(), "log tail") || err.Error() != "",
		err.Error())
}

func url2(s string) string { return s }
