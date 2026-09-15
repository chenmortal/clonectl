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

// NewManager builds a manager from the single configured address. In managed
// mode rcAddr is what rcd listens on (host:port; an optional URL scheme is
// stripped); RCURL is derived from it for probes and clients.
func NewManager(rcAddr, user, pass, bin string) *Manager {
	return &Manager{
		RCURL:   RCNormal(rcAddr),
		rcAddr:  listenAddr(rcAddr),
		user:    user,
		pass:    pass,
		bin:     bin,
		logFile: "rcd.log",
		pidFile: "rcd.pid",
	}
}

// RCNormal turns a configured address into a dialable URL: "0.0.0.0:5572" →
// "http://127.0.0.1:5572" (wildcard hosts map to loopback), "nas:5572" →
// "http://nas:5572"; a full "http(s)://…" URL keeps its scheme (parity with
// Python _rc_url). Must return a URL — callers use it for HTTP probes.
func RCNormal(addr string) string {
	scheme, rest := "http", addr
	if i := strings.Index(addr, "://"); i > 0 {
		scheme, rest = addr[:i], addr[i+3:]
	}
	u, err := url.Parse(scheme + "://" + rest)
	if err != nil || u.Hostname() == "" {
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

// listenAddr strips an optional URL scheme, leaving the host:port form that
// rcd's --rc-addr expects.
func listenAddr(addr string) string {
	if i := strings.Index(addr, "://"); i > 0 {
		return addr[i+3:]
	}
	return addr
}

// childEnv returns the parent environment minus this app's RCLONE_* config
// vars. rclone maps RCLONE_* env onto its flags — an inherited RCLONE_RC_ADDR
// overrides/double-applies our --rc-addr flag and makes rcd's rc server bind
// against itself (EADDRINUSE on a free port). The child is configured by
// flags only; unrelated vars (PATH, RCLONE_CONFIG_* remotes, …) pass through.
func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "RCLONE_RC_"),
			strings.HasPrefix(kv, "RCLONE_MANAGED="),
			strings.HasPrefix(kv, "RCLONE_BIN="):
			continue
		}
		out = append(out, kv)
	}
	return out
}

// probe outcomes for the RC endpoint.
type probeResult int

const (
	probeDown     probeResult = iota // nothing answering (conn refused/timeout)
	probeOK                          // /core/version 200 — a compatible rcd is live
	probeAuthFail                    // port alive but 401 — credentials differ
	probeForeign                     // port alive but no RC route (404/5xx…) — foreign HTTP service
)

// probe distinguishes "nothing there" from "something there that isn't our
// rcd". Only a refused/timed-out dial may lead to a new rcd launch: any live
// listener (401 mismatch, foreign HTTP service) already holds the port — the
// child's bind would just CRITICAL-exit with "address already in use".
func (m *Manager) probe() (probeResult, int) {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(m.RCURL, "/")+"/core/version", bytes.NewReader([]byte("{}")))
	if err != nil {
		return probeDown, 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(m.user, m.pass)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return probeDown, 0
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return probeOK, resp.StatusCode
	case http.StatusUnauthorized:
		return probeAuthFail, resp.StatusCode
	default:
		return probeForeign, resp.StatusCode
	}
}

// IsRunning probes the RC API readiness (200 on /core/version within 3s).
func (m *Manager) IsRunning() bool {
	res, _ := m.probe()
	return res == probeOK
}

// Start launches rcd unless it is already answering. Waits up to waitTimeout
// for readiness, polling every 500ms.
func (m *Manager) Start(waitTimeout time.Duration) error {
	switch res, status := m.probe(); res {
	case probeOK:
		slog.Info("rclone rcd: already running, skipping start")
		return nil
	case probeAuthFail:
		return fmt.Errorf(
			"rclone rcd 端口已被占用且认证不匹配（%s 返回 401）：请确认 RCLONE_RC_USER/RCLONE_RC_PASS 与已运行实例一致，或将 RCLONE_RC_ADDR 换一个端口",
			m.RCURL)
	case probeForeign:
		return fmt.Errorf(
			"rclone rcd 端口已被其他 HTTP 服务占用（%s 的 /core/version 返回 %d，非 rclone RC API）：请停止占用该端口的服务，或将 RCLONE_RC_ADDR 换一个端口",
			m.RCURL, status)
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
	cmd.Env = childEnv()
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

	// rclone v1.74 takes 2-3s to bind the socket after fork (logs "Serving
	// remote control on …" arrive that late). Don't probe before that —
	// early probes ECONNREFUSED and waste the budget.
	ready := time.Now().Add(2 * time.Second)
	deadline := time.Now().Add(waitTimeout)
	for {
		select {
		case <-waitCh:
			m.cmd = nil
			_ = os.Remove(m.pidFile)
			return fmt.Errorf("rclone rcd exited early: %s", tailFile(m.logFile, 300))
		default:
		}
		if time.Now().After(ready) && m.IsRunning() {
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
