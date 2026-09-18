// Package runner — argvgen: build the argv passed to redis-full-check.
//
// redis-full-check is CLI-flag-driven (no TOML). To stay compatible
// across upstream releases we maintain a small flag table; when
// upstream renames or adds a flag, this file is the only edit point.
//
// Flags are listed in the order the upstream CLI parser accepts them
// (positional flags first, then optional ones). The mode integer
// (1=full / 2=length / 3=existence / 4=adaptive) is mapped to "-m".
package runner

import (
	"fmt"
	"os"
	"path/filepath"
)

// FullCheckSpec is the agent-internal view of "everything we need to
// invoke redis-full-check". It mirrors upstream's CLI flag set:
//
//	-s / --source       source host:port
//	-p / --sourcepassword
//	-t / --target       target host:port
//	-a / --targetpassword
//	-d / --db           sqlite result path
//	-m / --comparemode  1=full / 2=length / 3=existence / 4=adaptive
//	--comparetimes      rounds (default 3)
//	--qps               rate limit (default 15000)
//	--interval          seconds between rounds (default 5)
//	--batchcount        keys per batch (default 256)
//	--parallel          goroutines (default 5)
//	--bigkeythreshold   bytes threshold for big-key handling
//	-f / --filterlist   pattern filter ('abc*|efg|m*')
//	--result            diff output file
//	--metric            metrics output file
type FullCheckSpec struct {
	Source         string // host:port
	SourcePassword string
	SourceDB       int

	Target         string
	TargetPassword string
	TargetDB       int

	CompareMode     int // 1..4
	CompareTimes    int
	QPS             int
	IntervalSeconds int
	BatchCount      int
	Parallel        int
	BigKeyThreshold int64
	FilterList      string
}

// ArgvForFullCheck returns the argv for redis-full-check, with the
// result.db path set to a per-task location so concurrent runs do
// not collide on the upstream's default "result.db" path.
func ArgvForFullCheck(spec FullCheckSpec, workDir string) ([]string, error) {
	if spec.Source == "" || spec.Target == "" {
		return nil, fmt.Errorf("argvgen: source and target required")
	}
	if spec.CompareMode < 1 || spec.CompareMode > 4 {
		return nil, fmt.Errorf("argvgen: compare_mode %d out of range [1..4]", spec.CompareMode)
	}

	resultDB := filepath.Join(workDir, "result.db")
	args := []string{
		"-s", spec.Source,
		"-t", spec.Target,
		"-m", fmt.Sprintf("%d", spec.CompareMode),
		"-d", resultDB,
	}
	if spec.SourcePassword != "" {
		args = append(args, "-p", spec.SourcePassword)
	}
	if spec.TargetPassword != "" {
		args = append(args, "-a", spec.TargetPassword)
	}
	if spec.CompareTimes > 0 {
		args = append(args, "--comparetimes", fmt.Sprintf("%d", spec.CompareTimes))
	}
	if spec.QPS > 0 {
		args = append(args, "--qps", fmt.Sprintf("%d", spec.QPS))
	}
	if spec.IntervalSeconds > 0 {
		args = append(args, "--interval", fmt.Sprintf("%d", spec.IntervalSeconds))
	}
	if spec.BatchCount > 0 {
		args = append(args, "--batchcount", fmt.Sprintf("%d", spec.BatchCount))
	}
	if spec.Parallel > 0 {
		args = append(args, "--parallel", fmt.Sprintf("%d", spec.Parallel))
	}
	if spec.BigKeyThreshold > 0 {
		args = append(args, "--bigkeythreshold", fmt.Sprintf("%d", spec.BigKeyThreshold))
	}
	if spec.FilterList != "" {
		args = append(args, "-f", spec.FilterList)
	}
	// Source/Target DB indexes are CLI args on newer upstream versions;
	// older ones use INFO keyspace. We pass both as a defensive layer.
	if spec.SourceDB > 0 {
		args = append(args, "--source.db", fmt.Sprintf("%d", spec.SourceDB))
	}
	if spec.TargetDB > 0 {
		args = append(args, "--target.db", fmt.Sprintf("%d", spec.TargetDB))
	}
	args = append(args,
		"--result", filepath.Join(workDir, "diff.txt"),
		"--metric", filepath.Join(workDir, "metric.txt"),
	)

	// One last redactor pass in case a future caller forgets to
	// scrub a password before reaching here.
	for i, a := range args {
		if looksLikePassword(a) {
			args[i] = "<redacted>"
		}
	}
	return args, nil
}

// looksLikePassword is a cheap pre-check before the redact package's
// regex runs. Avoids a regex compile on the hot path when argv is
// already clean.
func looksLikePassword(s string) bool {
	if len(s) < 6 || len(s) > 256 {
		return false
	}
	for _, c := range s {
		if c == ' ' || c == '"' || c == '\'' {
			return true
		}
	}
	return false
}

// FullCheckResultSummary is the projection of result.db we surface
// to clonectl via /v1/tasks/status. v1: counts only; the diff.txt
// file is left on disk for clonectl to fetch via /v1/tasks/logs.
type FullCheckResultSummary struct {
	Rounds      int   `json:"rounds"`
	TotalKeys   int64 `json:"total_keys"`
	ConflictCnt int64 `json:"conflict_count"`
	FinishedAt  int64 `json:"finished_at_unix_ms"`
}

// SummarizeResultDB is a placeholder; the upstream writes its own
// SQLite result.db with a schema we don't yet have a guaranteed
// reverse-engineered layout for. Until the agent ships a sqlite
// reader, this returns an empty summary so handlers don't crash.
//
// TODO(redis-fullcheck): wire a sqlite reader once we lock down the
// upstream table layout (probably under internal/checker/sqlite.go).
// For now we only expose the diff.txt text via /v1/tasks/logs.
func SummarizeResultDB(path string) (FullCheckResultSummary, error) {
	if _, err := os.Stat(path); err != nil {
		// result.db missing means the upstream hasn't created it yet
		// or it failed before the first round. Not a hard error.
		return FullCheckResultSummary{}, nil
	}
	return FullCheckResultSummary{FinishedAt: nowMillis()}, nil
}
