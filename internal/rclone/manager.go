package rclone

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Manager supervises a managed rclone rcd subprocess (RCLONE_MANAGED=true)
// or just health-checks an external one. Port of app/rclone/manager.py.
type Manager struct {
	RCURL      string // connection URL used by clients (0.0.0.0 → 127.0.0.1)
	rcAddr     string // listen address passed to rcd
	user, pass string
	bin        string
	logFile    string // rcd.log
	pidFile    string // rcd.pid

	cmd *exec.Cmd
}

// NewManager builds a manager. rcAddr is the listen address; rcURL the
// address clients dial.
func NewManager(rcURL, rcAddr, user, pass, bin string) *Manager {
	return &Manager{
		RCURL:   rcURL,
		rcAddr:  rcAddr,
		user:    user,
		pass:    pass,
		bin:     bin,
		logFile: "rcd.log",
		pidFile: "rcd.pid",
	}
}

// RCNormal maps wildcard listen hosts to a dialable loopback (parity with
// Python _rc_url).
func RCNormal(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return addr
	}
	host := u.Hostname()
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if port := u.Port(); port != "" {
		return host + ":" + port
	}
	return host
}

// IsRunning probes the RC API readiness (200 on /core/version within 3s).
func (m *Manager) IsRunning() bool {
	c := NewClient(m.RCURL, m.user, m.pass, 3*time.Second)
	defer c.Close()
	return c.Ping()
}

// Start launches rcd unless it is already answering. Waits up to waitTimeout
// for readiness, polling every 500ms.
func (m *Manager) Start(waitTimeout time.Duration) error {
	if m.IsRunning() {
		slog.Info("rclone rcd: already running, skipping start")
		return nil
	}

	log, err := os.OpenFile(m.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rcd log: %w", err)
	}
	defer log.Close()

	cmd := exec.Command(m.bin, "rcd",
		"--rc-addr="+m.rcAddr,
		"--rc-user="+m.user,
		"--rc-pass="+m.pass,
		"--rc-serve",
		"--rc-web-gui",
		"--rc-enable-metrics",
	)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rclone rcd: %w", err)
	}
	m.cmd = cmd
	_ = os.WriteFile(m.pidFile, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
	slog.Info("rclone rcd: started", "pid", cmd.Process.Pid, "addr", m.rcAddr)

	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		// Early exit? Signal(0) probes liveness without waiting.
		if cmd.Process != nil && cmd.Process.Signal(syscall.Signal(0)) != nil {
			_ = cmd.Process.Release()
			m.cmd = nil
			_ = os.Remove(m.pidFile)
			return fmt.Errorf("rclone rcd exited early, see %s", m.logFile)
		}
		if m.IsRunning() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	m.Stop()
	return fmt.Errorf("rclone rcd not ready within %s, see %s", waitTimeout, m.logFile)
}

// Stop terminates the managed process: SIGTERM → 10s → SIGKILL. External
// (unmanaged) rcd processes are left alone.
func (m *Manager) Stop() {
	if m.cmd == nil || m.cmd.Process == nil {
		return
	}
	pid := m.cmd.Process.Pid
	slog.Info("rclone rcd: stopping", "pid", pid)
	_ = m.cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		_, _ = m.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		slog.Warn("rclone rcd: SIGTERM timed out, killing", "pid", pid)
		_ = m.cmd.Process.Kill()
		<-done
	}
	m.cmd = nil
	_ = os.Remove(m.pidFile)
	slog.Info("rclone rcd: stopped")
}

// ReadPIDFile reads a pid file, returning 0 when absent or garbage.
func ReadPIDFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid); err != nil {
		return 0
	}
	return pid
}

// ProbeTCP is a raw host:port dial check (used by CLI status fallbacks).
func ProbeTCP(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
