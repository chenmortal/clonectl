package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// fakeScript is a tiny shell script that exits 0 and writes to stdout
// and stderr. Tests use it as a stand-in upstream binary.
const fakeScript = `#!/bin/sh
echo "[fake] total=42 finished=10"
echo "[fake] total=42 finished=20" >&2
echo "[fake] total=42 finished=42"
exit 0
`

func TestIsolation_AllocateWorkDir_Unique(t *testing.T) {
	tmp := t.TempDir()
	iso := NewIsolation(tmp, 20000)
	a, err := iso.AllocateWorkDir("task-aaa")
	if err != nil {
		t.Fatalf("AllocateWorkDir a: %v", err)
	}
	b, err := iso.AllocateWorkDir("task-bbb")
	if err != nil {
		t.Fatalf("AllocateWorkDir b: %v", err)
	}
	if a == b {
		t.Fatalf("expected different dirs, got both %s", a)
	}
	if !dirExists(a) || !dirExists(b) {
		t.Fatalf("dirs not created: a=%s b=%s", a, b)
	}
}

func TestIsolation_AllocateWorkDir_RejectsTraversal(t *testing.T) {
	iso := NewIsolation(t.TempDir(), 0)
	for _, bad := range []string{"../escape", "with/slash", `with\back`, ""} {
		if _, err := iso.AllocateWorkDir(bad); err == nil {
			t.Errorf("expected error for task_id %q", bad)
		}
	}
}

func TestIsolation_AllocatePort_Monotonic(t *testing.T) {
	iso := NewIsolation(t.TempDir(), 30000)
	a := iso.AllocatePort()
	b := iso.AllocatePort()
	c := iso.AllocatePort()
	if !(a < b && b < c) {
		t.Fatalf("ports not monotonic: %d %d %d", a, b, c)
	}
	if a != 30001 {
		t.Errorf("first port = %d, want 30001", a)
	}
}

func TestStart_FakeScript_LogsAndExit(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this platform")
	}
	bin := writeFakeScript(t, fakeScript)
	dir := filepath.Join(t.TempDir(), "task-fake")
	proc, err := store.Start(store.Options{
		Path:    bin,
		Args:    []string{},
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if proc.PID() == 0 {
		t.Fatalf("PID == 0")
	}
	// Wait for the fake to exit.
	deadline := time.Now().Add(5 * time.Second)
	for proc.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if proc.IsRunning() {
		t.Fatalf("fake script still running after 5s")
	}
	if got := proc.ExitCode(); got != 0 {
		t.Fatalf("ExitCode=%d, want 0", got)
	}
	tail := proc.LogTail()
	if len(tail) == 0 {
		t.Fatalf("LogTail empty; expected capture")
	}
}

func TestStart_MissingBinary_Errors(t *testing.T) {
	_, err := store.Start(store.Options{
		Path:    "/nonexistent/binary",
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for missing binary")
	}
}

func TestArgvForFullCheck_MinimalValid(t *testing.T) {
	spec := FullCheckSpec{
		Source:     "10.0.0.1:6379",
		Target:     "10.0.0.2:6379",
		CompareMode: 1,
	}
	args, err := ArgvForFullCheck(spec, t.TempDir())
	if err != nil {
		t.Fatalf("ArgvForFullCheck: %v", err)
	}
	if len(args) == 0 {
		t.Fatalf("empty argv")
	}
	joined := strings.Join(args, " ")
	for _, required := range []string{"-s", "10.0.0.1:6379", "-t", "10.0.0.2:6379", "-m", "1"} {
		if !strings.Contains(joined, required) {
			t.Errorf("argv missing %q: %v", required, args)
		}
	}
}

func TestArgvForFullCheck_RejectsBadMode(t *testing.T) {
	spec := FullCheckSpec{Source: "x", Target: "y", CompareMode: 9}
	if _, err := ArgvForFullCheck(spec, t.TempDir()); err == nil {
		t.Fatalf("expected error for compare_mode=9")
	}
}

func TestArgvForFullCheck_PreservesRequiredPasswordArg(t *testing.T) {
	// Documented behavior: redis-full-check's CLI parser requires the
	// password to be passed as `-p` (positional flag). The agent cannot
	// hide the password from /proc/<pid>/cmdline while the upstream is
	// running; mitigation lives in TOML/argv scrubbing for logs and
	// in the redact.Redact() pass on captured stdout before it hits
	// disk.
	spec := FullCheckSpec{
		Source: "x", Target: "y", CompareMode: 1,
		SourcePassword: "supersecret123",
	}
	args, err := ArgvForFullCheck(spec, t.TempDir())
	if err != nil {
		t.Fatalf("ArgvForFullCheck: %v", err)
	}
	found := false
	for i, a := range args {
		if a == "supersecret123" {
			// Confirm the password sits next to -p so operators reading
			// ps output can find it quickly.
			if i == 0 || args[i-1] != "-p" {
				t.Errorf("password at argv[%d] is not adjacent to -p: %v", i, args)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("password missing from argv: %v", args)
	}
}

func TestRenderTOML_ScrubsSecrets(t *testing.T) {
	spec := ShakeSpec{
		Reader: ShakeReader{
			Mode:     "sync_reader",
			Address:  "10.0.0.1:6379",
			Password: "leak-me",
		},
		Writer: ShakeWriter{Mode: "redis_writer", Address: "10.0.0.2:6379", Password: "leak-me-too"},
		Advanced: ShakeAdvanced{LogLevel: "info"},
	}
	path := filepath.Join(t.TempDir(), "shake.toml")
	if err := RenderTOML(spec, path); err != nil {
		t.Fatalf("RenderTOML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "leak-me") || strings.Contains(content, "leak-me-too") {
		t.Errorf("password leaked into TOML: %s", content)
	}
}

// --- helpers ---

func writeFakeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return p
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
