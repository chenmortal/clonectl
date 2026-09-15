package rclone

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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
	WebGUI     bool   // pass --rc-web-gui (needs GitHub on first run; optional)

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

// RCNormal maps wildcard listen hosts to a dialable loopback while
// preserving the scheme (parity with Python _rc_url). Must return a URL —
// callers use it for HTTP probes.
func RCNormal(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return addr
	}
	host := u.Hostname()
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}
	return u.String()
}

// probe outcomes for the RC endpoint.
type probeResult int

const (
	probeDown      probeResult = iota // nothing answering (conn refused/timeout/5xx)
	probeOK                           // /core/version 200 — a compatible rcd is live
	probeAuthFail                     // port alive but 401 — credentials differ
)

// probe distinguishes "nothing there" from "something there but auth
// mismatch". Both are non-running for our purposes, but the latter must NOT
// trigger a new rcd launch (the port is taken — bind would fail).
func (m *Manager) probe() probeResult {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(m.RCURL, "/")+"/core/version", bytes.NewReader([]byte("{}")))
	if err != nil {
		return probeDown
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(m.user, m.pass)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return probeDown
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return probeAuthFail
	}
	if resp.StatusCode == http.StatusOK {
		return probeOK
	}
	return probeDown
}

// IsRunning probes the RC API readiness (200 on /core/version within 3s).
func (m *Manager) IsRunning() bool {
	return m.probe() == probeOK
}

// Start launches rcd unless it is already answering. Waits up to waitTimeout
// for readiness, polling every 500ms.
func (m *Manager) Start(waitTimeout time.Duration) error {
	switch m.probe() {
	case probeOK:
		slog.Info("rclone rcd: already running, skipping start")
		return nil
	case probeAuthFail:
		return fmt.Errorf(
			"rclone rcd 端口已被占用且认证不匹配（%s 返回 401）：请确认 RCLONE_RC_USER/RCLONE_RC_PASS 与已运行实例一致，或将 RCLONE_RC_ADDR 换一个端口",
			m.RCURL)
	case probeDown:
		// proceed to launch
	}

	log, err := os.OpenFile(m.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rcd log: %w", err)
	}
	defer log.Close()

	args := []string{
		"rcd",
		"--rc-addr=" + m.rcAddr,
		"--rc-user=" + m.user,
		"--rc-pass=" + m.pass,
		"--rc-serve",
		"--rc-enable-metrics",
	}
	// --rc-web-gui downloads a React WebUI from GitHub on first run and
	// CRITICAL-exits when unreachable — off by default; this console IS the
	// web UI. Opt in with RCLONE_RC_WEB_GUI=true when actually wanted.
	if m.WebGUI {
		args = append(args, "--rc-web-gui")
	}
	cmd := exec.Command(m.bin, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rclone rcd: %w", err)
	}
	m.cmd = cmd
	_ = os.WriteFile(m.pidFile, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
	slog.Info("rclone rcd: started", "pid", cmd.Process.Pid, "addr", m.rcAddr)

	// Reap the child as soon as it dies so early-exit is detectable
	// (Signal(0) succeeds on zombies and can't be used here).
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	deadline := time.Now().Add(waitTimeout)
	for {
		select {
		case <-waitCh:
			m.cmd = nil
			_ = os.Remove(m.pidFile)
			return fmt.Errorf("rclone rcd exited early: %s", tailFile(m.logFile, 300))
		default:
		}
		if m.IsRunning() {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	m.kill()
	return fmt.Errorf("rclone rcd not ready within %s, see %s (log tail: %s)",
		waitTimeout, m.logFile, tailFile(m.logFile, 300))
}

// tailFile returns the last n bytes of path (best effort, for error text).
func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}

// kill terminates the managed process: SIGTERM → 10s → SIGKILL.
func (m *Manager) kill() {
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
	slog.Info("rclone rcd: stopped")
}

// Stop releases the managed rcd (no-op when we never spawned one) and
// removes the pid file.
func (m *Manager) Stop() {
	if m.cmd == nil {
		return
	}
	m.kill()
	m.cmd = nil
	_ = os.Remove(m.pidFile)
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

// ProbeTCP removed: status CLI uses *RcloneClient.Ping which is the
// authoritative RC API check; raw TCP dial would give a false positive
// for non-rclone services on the same port.
