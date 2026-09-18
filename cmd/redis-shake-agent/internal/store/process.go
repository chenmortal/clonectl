// Package store owns the lifecycle of upstream CLI processes.
//
// process.go defines the thin exec.Cmd wrapper shared by the
// redis-shake and redis-full-check runners. The wrapper:
//
//   - captures stdout & stderr separately into ringbuffers,
//   - forwards SIGTERM on graceful stop and SIGKILL after a grace
//     window (Go 1.20+ exec.Cmd.Cancel / WaitDelay),
//   - records the child PID so /v1/tasks/status can report it,
//   - leaves log rotation to the caller (the agent's retention
//     loop in store/taskstore.go).
//
// The wrapper is intentionally NOT aware of upstream tool semantics
// (TOML shape, argv flags, progress meaning). Those live in the
// runner package so the upstream-coupling surface stays small.
package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Process is a running upstream CLI child. It is the unit of state
// held by store.TaskStore and referenced by every /v1/tasks/* handler.
type Process struct {
	mu sync.Mutex

	// cmd is the live *exec.Cmd. nil after the process has fully exited
	// and wait() has returned.
	cmd *exec.Cmd

	// pid is captured at Start time so we can report it before the
	// goroutines have observed exit.
	pid int

	// startedAt / endedAt bracket the lifecycle for /v1/tasks/status.
	startedAt time.Time
	endedAt   time.Time
	exitCode  int

	// stdout / stderr ringbuffers keep the last N bytes of each stream
	// in memory so /v1/tasks/logs can serve them without touching
	// disk. Disk holds the full tail file.
	stdout *ringbuf
	stderr *ringbuf

	// logFile is the on-disk tail of stdout (stderr intermingled for
	// now — split logs are an upstream-tool concern, not the agent's).
	logFile *os.File
	logPath string
}

// Options configure Start. Path is the upstream binary's absolute
// location; Args is the full argv (excluding argv[0]); WorkDir is the
// per-task working directory.
type Options struct {
	Path           string
	Args           []string
	WorkDir        string
	Env            []string
	RingBufferSize int // bytes; default 64 KiB
	WaitDelay      time.Duration
}

// Start launches the upstream process and returns a *Process bound to
// the task's working directory. The process is detached from the
// agent's stdin but inherits a fresh /dev/null-equivalent so it never
// blocks waiting for tty input.
func Start(opts Options) (*Process, error) {
	if opts.Path == "" {
		return nil, errors.New("runner: empty binary path")
	}
	if _, err := os.Stat(opts.Path); err != nil {
		return nil, fmt.Errorf("runner: binary %s not found: %w", opts.Path, err)
	}
	if opts.WorkDir == "" {
		return nil, errors.New("runner: empty work dir")
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("runner: mkdir work_dir: %w", err)
	}
	if opts.RingBufferSize <= 0 {
		opts.RingBufferSize = 64 * 1024
	}
	if opts.WaitDelay <= 0 {
		opts.WaitDelay = 30 * time.Second
	}

	logPath := filepath.Join(opts.WorkDir, "stdout.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("runner: open log file: %w", err)
	}

	cmd := exec.Command(opts.Path, opts.Args...)
	cmd.Dir = opts.WorkDir
	cmd.Env = append(os.Environ(), opts.Env...)
	// Detach from any controlling tty so SIGINT to the agent does not
	// cascade into the child via the foreground process group.
	cmd.SysProcAttr = detachedProcAttr()

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("runner: stdout pipe: %w", err)
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("runner: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("runner: start %s: %w", opts.Path, err)
	}

	p := &Process{
		cmd:       cmd,
		pid:       cmd.Process.Pid,
		startedAt: time.Now(),
		stdout:    newRingbuf(opts.RingBufferSize),
		stderr:    newRingbuf(opts.RingBufferSize),
		logFile:   lf,
		logPath:   logPath,
	}

	// Wire WaitDelay → SIGKILL escalation.
	cmd.WaitDelay = opts.WaitDelay
	cmd.Cancel = func() error {
		// First signal: SIGTERM (graceful). The process group leader
		// receives it; if children have been spawned into the same
		// group they inherit the signal too.
		if cmd.Process != nil {
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		return nil
	}

	// Drain pipes into ringbuffers + the on-disk log. We use a single
	// goroutine pair; Wait() is called after both pipes reach EOF.
	var wg sync.WaitGroup
	wg.Add(2)
	go p.pump(stdoutR, &wg, p.stdout)
	go p.pump(stderrR, &wg, p.stderr)

	go func() {
		wg.Wait()
		err := cmd.Wait()
		p.mu.Lock()
		p.endedAt = time.Now()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				p.exitCode = ee.ExitCode()
			} else {
				p.exitCode = -1
			}
		} else {
			p.exitCode = 0
		}
		p.cmd = nil
		p.mu.Unlock()
		_ = p.logFile.Close()
	}()

	return p, nil
}

// PID returns the child PID. 0 before Start (should not happen) or
// after the process has been reaped.
func (p *Process) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// StartedAt / EndedAt expose the lifecycle timestamps to handlers.
func (p *Process) StartedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedAt
}

// EndedAt returns the zero value if the process is still running.
func (p *Process) EndedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.endedAt
}

// ExitCode returns -1 while the process is running.
func (p *Process) ExitCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.endedAt.IsZero() {
		return -1
	}
	return p.exitCode
}

// IsRunning reports whether the child has not yet exited.
func (p *Process) IsRunning() bool {
	return p.ExitCode() == -1
}

// LogPath returns the on-disk tail path. Used by /v1/tasks/logs for
// serving the full file (the ringbuffer is the head).
func (p *Process) LogPath() string {
	return p.logPath
}

// LogTail returns the last ringbufBytes of stdout captured in memory.
// Used by progress scrapers that need to regex over the recent log
// without touching disk. The returned slice is a copy — callers may
// retain it freely.
func (p *Process) LogTail() []byte {
	if p.stdout == nil {
		return nil
	}
	return p.stdout.Bytes()
}

// Stop sends SIGTERM and waits for the child to exit. If grace elapses
// the agent escalates to SIGKILL via cmd.WaitDelay (set in Start).
// Calling Stop on an already-stopped process is a no-op.
func (p *Process) Stop(ctx context.Context) error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// ESRCH = already exited; treat as success.
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	// Wait for WaitDelay (set on cmd) or until ctx expires. The agent
	// uses WaitDelay internally; we still observe ctx for callers that
	// want a tighter deadline.
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	case <-p.doneChan():
		return nil
	}
}

// doneChan returns a channel that closes when the process exits.
func (p *Process) doneChan() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		for {
			p.mu.Lock()
			done := p.cmd == nil
			p.mu.Unlock()
			if done {
				close(ch)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return ch
}

// ReadLogs returns the tail of the on-disk log file starting at
// offset. If offset is beyond EOF the returned chunk is empty.
func (p *Process) ReadLogs(offset int64, limit int) ([]byte, int64, bool, error) {
	f, err := os.Open(p.logPath)
	if err != nil {
		return nil, offset, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, offset, false, err
	}
	size := st.Size()
	if offset >= size {
		return nil, offset, true, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, false, err
	}
	var rdr io.Reader = f
	if limit > 0 {
		rdr = io.LimitReader(f, int64(limit))
	}
	buf := &bytes.Buffer{}
	n, err := io.Copy(buf, rdr)
	if err != nil {
		return nil, offset, false, err
	}
	nextOffset := offset + n
	eof := nextOffset >= size && !p.IsRunning()
	return buf.Bytes(), nextOffset, eof, nil
}

// pump copies from src into both the ringbuffer and the on-disk log.
// Any error reading src (typically io.ErrClosedPipe after the child
// exits) is swallowed — exitCode/endedAt are tracked separately.
func (p *Process) pump(src io.Reader, wg *sync.WaitGroup, rb *ringbuf) {
	defer wg.Done()
	buf := make([]byte, 4*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			rb.Write(chunk)
			_, _ = p.logFile.Write(chunk)
		}
		if err != nil {
			return
		}
	}
}

// ringbuf is a tiny ring buffer used to keep the last N bytes of a
// stream in memory for cheap preview serving.
type ringbuf struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newRingbuf(cap int) *ringbuf { return &ringbuf{buf: make([]byte, 0, cap), cap: cap} }

func (r *ringbuf) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
}

func (r *ringbuf) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}
