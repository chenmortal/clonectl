// redis-shake-agent — e2e_test.go: drive a real agent binary end to
// end by pointing its config at a fake "upstream" shell script that
// emits the canonical redis-shake progress shape. Then query the
// agent's /v1/tasks/status over HTTP and assert the AgentTaskID +
// run state surface correctly.
//
// This is the closest analogue to plan §7.2's docker-based end-to-end
// that we can run without docker / without the real upstream binary.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenmortal/redis-shake-agent/internal/config"
	"github.com/chenmortal/redis-shake-agent/internal/runner"
	"github.com/chenmortal/redis-shake-agent/internal/server"
	"github.com/chenmortal/redis-shake-agent/internal/store"
)

const fakeShakeScript = `#!/bin/sh
# Fake redis-shake that emits progress on stdout and exits 0 after a
# short sleep so the agent's stdout capture has time to land.
echo "[fake] total=42 finished=10"
sleep 0.2
echo "[fake] total=42 finished=20"
sleep 0.2
echo "[fake] total=42 finished=42"
exit 0
`

const fakeFullCheckScript = `#!/bin/sh
echo "[fake] round=1 total=100 processed=50 conflicts=0"
sleep 0.1
echo "[fake] round=2 total=100 processed=100 conflicts=2"
echo "[fake] all 100 keys finished conflicts=2"
exit 0
`

func writeFakeBinary(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return p
}

// TestE2E_AgentSubmitAndStatus: bring up the agent's gin router
// in-process, submit a task via HTTP, then poll /v1/tasks/status.
func TestE2E_AgentSubmitAndStatus(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this platform")
	}
	fakeBin := writeFakeBinary(t, fakeShakeScript)

	// Configure the agent to run our fake binary as the redis-shake
	// slot. WorkRoot points at a temp dir; LogRoot is unused here
	// (the agent's retention sweep is what reads it).
	workRoot := t.TempDir()
	cfg := runner.AgentConfig{
		Shake: runner.ShakeConfig{Binary: fakeBin, LogRetentionDays: 7},
		LogRetentionDays: 7,
	}
	tasks := store.NewTaskStore()
	iso := runner.NewIsolation(workRoot, 0)

	// Build the gin engine via the server package and wrap with
	// httptest so we can hit it over HTTP without binding a port.
	engine := server.New(cfg, tasks, iso)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	// Submit.
	submitBody := `{"tool":"redis-shake","task_id":"t-e2e-1","mode":"sync_reader","config":{"source":{"address":"10.0.0.1:6379"},"target":{"address":"10.0.0.2:6379"},"extra":{}}}`
	resp := postJSON(t, srv.URL+"/v1/tasks/submit", submitBody)
	if !resp.ok {
		t.Fatalf("submit: %s", resp.body)
	}
	var submitResp struct {
		Data struct {
			TaskID string `json:"task_id"`
			PID    int    `json:"pid"`
			State  string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp.body), &submitResp); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if submitResp.Data.TaskID != "t-e2e-1" {
		t.Errorf("TaskID=%s", submitResp.Data.TaskID)
	}
	if submitResp.Data.PID <= 0 {
		t.Errorf("PID=%d (expected > 0)", submitResp.Data.PID)
	}
	if submitResp.Data.State != "running" {
		t.Errorf("State=%s, want running", submitResp.Data.State)
	}

	// Poll /v1/tasks/status a few times until the child exits or 2s elapse.
	deadline := time.Now().Add(2 * time.Second)
	var finalState string
	for time.Now().Before(deadline) {
		s := getJSON(t, srv.URL+"/v1/tasks/status?task_id=t-e2e-1")
		var statusResp struct {
			Data struct {
				State    string `json:"state"`
				PID      int    `json:"pid"`
				ExitCode int    `json:"exit_code"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(s), &statusResp); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		finalState = statusResp.Data.State
		if finalState == "success" || finalState == "failed" {
			if statusResp.Data.ExitCode != 0 {
				t.Errorf("expected exit_code=0 on success, got %d", statusResp.Data.ExitCode)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalState != "success" {
		t.Errorf("finalState=%s, want success (fake script exited 0)", finalState)
	}
}

// TestE2E_AgentRejectsUnsupportedMode: a Submit with mode="scan_reader"
// is rejected by the in-process driver validation when no upstream
// binary is configured to support it. We pass it through the agent's
// HTTP handler, which currently does NOT pre-validate against mode
// (the agent trusts clonectl's ValidateSpec). To exercise the path
// where the agent runs but the upstream exits non-zero, we leave
// that for the Submit path on a missing binary.
//
// Here we just confirm the agent refuses an unsupported binary path.
func TestE2E_AgentMissingBinary(t *testing.T) {
	cfg := runner.AgentConfig{
		Shake: runner.ShakeConfig{Binary: "/nonexistent/path", LogRetentionDays: 7},
		LogRetentionDays: 7,
	}
	tasks := store.NewTaskStore()
	iso := runner.NewIsolation(t.TempDir(), 0)
	engine := server.New(cfg, tasks, iso)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/v1/tasks/submit",
		`{"tool":"redis-shake","task_id":"t-miss","mode":"sync_reader","config":{}}`)
	if resp.ok {
		t.Fatalf("submit should have failed with missing binary, got: %s", resp.body)
	}
	if !strings.Contains(resp.body, "upstream_crash") &&
		!strings.Contains(resp.body, "no such file") &&
		!strings.Contains(resp.body, "not found") {
		t.Errorf("error body should reference binary not found: %s", resp.body)
	}
}

// TestE2E_AgentFullCheck: a FullCheck run lands on the agent and the
// final state reflects exit_code 0.
func TestE2E_AgentFullCheck(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this platform")
	}
	fakeBin := writeFakeBinary(t, fakeFullCheckScript)
	cfg := runner.AgentConfig{
		FullCheck: runner.FullCheckConfig{Binary: fakeBin, LogRetentionDays: 7},
		LogRetentionDays: 7,
	}
	tasks := store.NewTaskStore()
	iso := runner.NewIsolation(t.TempDir(), 0)
	engine := server.New(cfg, tasks, iso)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	submitBody := `{"tool":"redis-fullcheck","task_id":"fc-e2e","mode":"1","config":{"source":{"address":"10.0.0.1:6379"},"target":{"address":"10.0.0.2:6379"}}}`
	resp := postJSON(t, srv.URL+"/v1/tasks/submit", submitBody)
	if !resp.ok {
		t.Fatalf("submit: %s", resp.body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := getJSON(t, srv.URL+"/v1/tasks/status?task_id=fc-e2e")
		if strings.Contains(s, `"state":"success"`) {
			return
		}
		if strings.Contains(s, `"state":"failed"`) {
			t.Fatalf("FullCheck run failed: %s", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("FullCheck run did not reach terminal state within 2s")
}

// --- helpers ---

type httpResp struct {
	ok   bool
	body string
}

func postJSON(t *testing.T, url, body string) httpResp {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 3 * time.Second}
	r, err := c.Do(req)
	if err != nil {
		return httpResp{ok: false, body: err.Error()}
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return httpResp{ok: r.StatusCode/100 == 2, body: string(b)}
}

func getJSON(t *testing.T, url string) string {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Second}
	r, err := c.Get(url)
	if err != nil {
		return ""
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// --- test-only router bootstrap ---

// We import server.New from the agent's internal/server package
// directly to build the same gin.Engine the binary uses in
// production. Keeping the agent tests in the same Go module as the
// binary (cmd/redis-shake-agent/) means there is no cross-process
// IPC needed for these tests — they share types and helpers.
var _ = server.New // keep the import line; New is used above.

// --- config sanity test (no I/O) ---

func TestConfig_LoadMissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatalf("expected error for missing config file")
	}
}
