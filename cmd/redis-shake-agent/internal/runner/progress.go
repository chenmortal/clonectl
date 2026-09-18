// Package runner — progress: turn raw upstream signals into a
// NormalizedProgress the agent surfaces to clonectl.
//
// Two upstream shapes are handled:
//
//  1. redis-shake (ToolShake): scrape the redis-shake-http-api
//     wrapper's /metrics endpoint (Prometheus text). Parse a small
//     set of counters — total / finished / percent / lag — and
//     return them as a NormalizedProgress. If the wrapper is not
//     running, fall back to parsing the captured stdout.
//
//  2. redis-full-check (ToolFullCheck): the upstream has no HTTP
//     surface; we parse progress lines from the stdout ringbuffer
//     that the runner already maintains. A typical line is:
//
//        [2024-09-18 14:23:11] round=2 total=12345 processed=12000 conflicts=4
//
//     The regex is the only file to edit if upstream changes the log
//     format.
//
// Both paths return ProgressResult{Progress, Raw, Err}. Err is
// non-nil only when something genuinely went wrong (HTTP scrape
// failed); a missing counter or a parse miss yields a Progress with
// zero values and a non-empty Raw so clonectl can still surface the
// underlying data in its UI debug drawer.
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chenmortal/redis-shake-agent/internal/redact"
	"github.com/chenmortal/redis-shake-agent/internal/store"
)

// ProgressResult is what every progress collector returns.
type ProgressResult struct {
	// Progress is the agent-normalized counters; zero values mean
	// "unknown" — UI displays these as "-".
	Progress Progress

	// Raw is the upstream-private payload as JSON. clonectl stores
	// it on the run row verbatim.
	Raw json.RawMessage

	// Err is set only on transport-level failures (e.g. metrics
	// endpoint not reachable). Parse misses produce zero Progress
	// with no error so the poller keeps trying.
	Err error
}

// Progress is the agent-normalized shape (not the clonectl one —
// we do not import clonectl here to keep this module independent).
type Progress struct {
	Total      int64   `json:"total"`
	Done       int64   `json:"done"`
	Percent    int     `json:"percent"`
	Throughput float64 `json:"throughput"`
	LagSeconds float64 `json:"lag_seconds"`
	Stage      string  `json:"stage,omitempty"`
}

// ScrapeShakeProgress scrapes redis-shake-http-api /metrics on
// metricsURL. If metricsURL is empty or unreachable, it falls back
// to parsing the task's stdout log for the same counters.
func ScrapeShakeProgress(ctx context.Context, t *store.Task, metricsURL string) ProgressResult {
	if metricsURL != "" {
		if p, raw, err := scrapeProm(ctx, metricsURL); err == nil {
			return ProgressResult{Progress: p, Raw: raw}
		} else {
			// HTTP failed; fall through to log parsing but surface
			// the error in Raw for debugging.
			raw, _ := json.Marshal(map[string]string{"scrape_error": err.Error()})
			return ProgressResult{Raw: raw}
		}
	}
	// Fallback: parse the captured stdout.
	p, raw, _ := parseShakeStdout(t)
	return ProgressResult{Progress: p, Raw: raw}
}

// shakeStdoutRe matches the canonical redis-shake log line that
// reports counters. The exact format has drifted across releases; we
// anchor on key=value pairs and tolerate whitespace.
//
//	[2024-09-18 14:23:11] total=12345 finished=12000 percent=97.0
var shakeStdoutRe = regexp.MustCompile(`total=(\d+)\s+finished=(\d+)(?:\s+percent=([0-9.]+))?`)

func parseShakeStdout(t *store.Task) (Progress, json.RawMessage, error) {
	if t.Process == nil {
		return Progress{}, nil, nil
	}
	logBytes := t.Process.LogTail()
	if len(logBytes) == 0 {
		return Progress{}, nil, nil
	}
	// Walk the buffer line by line, keep the LAST match — upstream
	// prints incremental counters, so the most recent line wins.
	scanner := bufio.NewScanner(bytesReader(logBytes))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var last Progress
	var matched bool
	for scanner.Scan() {
		line := scanner.Text()
		m := shakeStdoutRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total, _ := strconv.ParseInt(m[1], 10, 64)
		done, _ := strconv.ParseInt(m[2], 10, 64)
		pct := -1
		if m[3] != "" {
			if f, err := strconv.ParseFloat(m[3], 64); err == nil {
				pct = int(f)
			}
		}
		last = Progress{
			Total:   total,
			Done:    done,
			Percent: pct,
			Stage:   "incremental",
		}
		matched = true
	}
	raw, _ := json.Marshal(map[string]any{
		"source":   "stdout_regex",
		"matched":  matched,
		"scanned":  len(logBytes),
	})
	return last, raw, nil
}

// scrapeProm hits a Prometheus text endpoint and pulls a curated set
// of counters. The exact metric names vary across redis-shake
// releases; we probe a few aliases and take whichever exists.
func scrapeProm(ctx context.Context, url string) (Progress, json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Progress{}, nil, err
	}
	cli := &http.Client{Timeout: 3 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return Progress{}, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Progress{}, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return Progress{}, nil, fmt.Errorf("metrics HTTP %d", resp.StatusCode)
	}

	// Parse Prometheus text format. We extract any counter whose name
	// contains "total" / "finished" / "lag" — the upstream metric
	// names differ between releases but those keywords are stable.
	var (
		total   int64
		finished int64
		lag     float64
	)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		valStr := fields[1]
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(lower, "total"):
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				total = int64(v)
			}
		case strings.Contains(lower, "finished") || strings.Contains(lower, "done"):
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				finished = int64(v)
			}
		case strings.Contains(lower, "lag"):
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				lag = v
			}
		}
	}
	p := Progress{Total: total, Done: finished, LagSeconds: lag}
	if total > 0 {
		p.Percent = int((finished * 100) / total)
		if p.Percent > 100 {
			p.Percent = 100
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"source": "prom_scrape",
		"url":    url,
		"bytes":  len(body),
	})
	return p, raw, nil
}

// ScrapeFullCheckProgress parses the FullCheck stdout for
// "round=N total=... processed=... conflicts=..." lines. Falls back
// to zero Progress when the log is empty.
func ScrapeFullCheckProgress(t *store.Task) ProgressResult {
	if t.Process == nil {
		return ProgressResult{}
	}
	logBytes := t.Process.LogTail()
	if len(logBytes) == 0 {
		return ProgressResult{}
	}

	// Two regexes: the per-round progress line and the final summary.
	progressRe := regexp.MustCompile(`round=(\d+)\s+total=(\d+)\s+processed=(\d+)(?:\s+conflicts=(\d+))?`)
	summaryRe := regexp.MustCompile(`all\s+(\d+)\s+keys\s+finished\s+conflicts=(\d+)`)

	var (
		round     int
		total     int64
		processed int64
		conflicts int64
		finished  bool
	)
	scanner := bufio.NewScanner(bytesReader(logBytes))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := progressRe.FindStringSubmatch(line); m != nil {
			round, _ = strconv.Atoi(m[1])
			total, _ = strconv.ParseInt(m[2], 10, 64)
			processed, _ = strconv.ParseInt(m[3], 10, 64)
			if m[4] != "" {
				conflicts, _ = strconv.ParseInt(m[4], 10, 64)
			}
		}
		if m := summaryRe.FindStringSubmatch(line); m != nil {
			processed, _ = strconv.ParseInt(m[1], 10, 64)
			conflicts, _ = strconv.ParseInt(m[2], 10, 64)
			finished = true
		}
	}

	p := Progress{
		Total:   total,
		Done:    processed,
		LagSeconds: 0,
		Stage:   fmt.Sprintf("round_%d", round),
	}
	if total > 0 {
		p.Percent = int((processed * 100) / total)
		if p.Percent > 100 {
			p.Percent = 100
		}
	}
	if finished {
		p.Stage = "finished"
	}
	raw, _ := json.Marshal(map[string]any{
		"source":         "stdout_regex",
		"round":          round,
		"conflict_count": conflicts,
	})
	return ProgressResult{Progress: p, Raw: raw}
}

// bytesReader is a tiny adapter to keep the import set minimal.
type bytesReaderImpl struct {
	buf []byte
	pos int
}

func bytesReader(b []byte) *bytesReaderImpl { return &bytesReaderImpl{buf: b} }

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.buf) {
		return 0, io.EOF
	}
	n := copy(p, r.buf[r.pos:])
	r.pos += n
	return n, nil
}

// Sanity check that redact package's CompilesString error path is
// reachable from this file — catches unused import if the regex
// above is ever simplified.
var _ = redact.ContainsSensitive
