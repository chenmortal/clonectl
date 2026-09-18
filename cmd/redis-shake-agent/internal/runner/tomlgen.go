// Package runner — tomlgen: convert a clonectl Spec into a
// redis-shake TOML config file.
//
// Why a generator instead of letting clonectl ship TOML? Because the
// whole point of the agent is to absorb upstream TOML/argv drift:
// clonectl hands us a structured Spec, the agent renders TOML using
// the schema the locally-pinned redis-shake understands. If upstream
// renames a key or adds a new section, the only file to edit is here.
//
// The generated TOML intentionally includes ONLY the keys the agent
// knows about — we do not round-trip arbitrary Extra data through
// here, because Extra is tool-agnostic JSON. Use the redis-shake
// `--conf` file as the canonical home for shake-specific knobs.
package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenmortal/redis-shake-agent/internal/redact"
)

// ShakeSpec is the agent-internal view of "everything we need to
// render a redis-shake TOML". It mirrors the upstream sections
// [sync_reader]/[redis_writer]/[scan_reader]/[advanced]/[filter] but
// is hand-curated for the keys we actually exercise.
//
// When the upstream schema changes, this struct is the single edit
// point — the Spec lives in clonectl and is tool-agnostic.
type ShakeSpec struct {
	// Reader selects the source. Exactly one of these is non-nil.
	Reader ShakeReader

	// Writer selects the destination. Exactly one of these is non-nil.
	Writer ShakeWriter

	// Advanced carries shake's [advanced] section knobs.
	Advanced ShakeAdvanced

	// Filter applies key-pattern / db filters (optional).
	Filter ShakeFilter
}

// ShakeReader is the discriminated union over sync / rdb / scan.
type ShakeReader struct {
	// Mode is one of "sync_reader", "rdb_reader", "scan_reader".
	Mode string

	// Common across all readers
	Address  string
	Username string
	Password string

	// Sync-only knobs
	Sync SyncReaderOpts

	// RDB-only knobs
	RDB RDBReaderOpts

	// Scan-only knobs
	Scan ScanReaderOpts
}

// SyncReaderOpts covers [sync_reader] knobs.
type SyncReaderOpts struct {
	// Snapshot is true to take an initial snapshot before PSYNC.
	Snapshot bool
	// Parallel is the number of parallel restore workers.
	Parallel int
}

// RDBReaderOpts covers [rdb_reader] knobs.
type RDBReaderOpts struct {
	// Path is the RDB file location on the source host filesystem.
	Path string
}

// ScanReaderOpts covers [scan_reader] knobs.
type ScanReaderOpts struct {
	// Count is SCAN COUNT per iteration.
	Count int
	// KeysPerRequest caps a single DUMP/RESTORE pair.
	KeysPerRequest int64
}

// ShakeWriter is the destination. v1 supports redis_writer only.
type ShakeWriter struct {
	Mode        string // "redis_writer" | "file_writer"
	Address     string
	Username    string
	Password    string
	Parallel    int
	Compression bool
}

// ShakeAdvanced covers the [advanced] section (log level, metrics,
// restore parallel, etc).
type ShakeAdvanced struct {
	LogLevel    string
	LogFile     string
	Metrics     bool
	Pprof       bool
	RestoreParallel int
}

// ShakeFilter covers [filter] (key-pattern db filter).
type ShakeFilter struct {
	// KeyPrefixes restricts sync to keys matching any of these prefixes.
	KeyPrefixes []string
	// DBBlacklist excludes these db indexes from sync.
	DBBlacklist []string
}

// RenderTOML serializes spec into the canonical redis-shake config
// format and writes it to path. The file is overwritten on each
// invocation — the caller (handler) has already determined that the
// spec has changed.
//
// Notes on the format:
//
//   - We emit section headers in the order upstream docs list them.
//   - String values are quoted only when they contain characters that
//     would break TOML bareword parsing.
//   - Passwords are redacted on disk in the rendered TOML (we still
//     pass the live password via env var / argv when launching the
//     binary so the on-disk file stays secret-free).
func RenderTOML(spec ShakeSpec, path string) error {
	var b bytes.Buffer

	// --- reader ---
	fmt.Fprintf(&b, "[%s]\n", spec.Reader.Mode)
	fmt.Fprintf(&b, "address = %q\n", spec.Reader.Address)
	if spec.Reader.Username != "" {
		fmt.Fprintf(&b, "username = %q\n", spec.Reader.Username)
	}
	if spec.Reader.Password != "" {
		// Secret lives in env, not on disk. We still emit a placeholder
		// so upstream parses the file; the agent supplies the real
		// password via REDIS_SHAKE_SOURCE_PASSWORD-style env vars at
		// exec time.
		fmt.Fprintf(&b, "password = %q\n", "${SOURCE_PASSWORD}")
	}

	switch spec.Reader.Mode {
	case "sync_reader":
		fmt.Fprintf(&b, "snapshot = %t\n", spec.Reader.Sync.Snapshot)
		if spec.Reader.Sync.Parallel > 0 {
			fmt.Fprintf(&b, "parallel = %d\n", spec.Reader.Sync.Parallel)
		}
	case "rdb_reader":
		fmt.Fprintf(&b, "path = %q\n", spec.Reader.RDB.Path)
	case "scan_reader":
		if spec.Reader.Scan.Count > 0 {
			fmt.Fprintf(&b, "count = %d\n", spec.Reader.Scan.Count)
		}
		if spec.Reader.Scan.KeysPerRequest > 0 {
			fmt.Fprintf(&b, "keys_per_request = %d\n", spec.Reader.Scan.KeysPerRequest)
		}
	}

	// --- writer ---
	if spec.Writer.Mode != "" {
		fmt.Fprintf(&b, "\n[%s]\n", spec.Writer.Mode)
		fmt.Fprintf(&b, "address = %q\n", spec.Writer.Address)
		if spec.Writer.Username != "" {
			fmt.Fprintf(&b, "username = %q\n", spec.Writer.Username)
		}
		if spec.Writer.Password != "" {
			fmt.Fprintf(&b, "password = %q\n", "${TARGET_PASSWORD}")
		}
		if spec.Writer.Parallel > 0 {
			fmt.Fprintf(&b, "parallel = %d\n", spec.Writer.Parallel)
		}
		if spec.Writer.Compression {
			fmt.Fprintf(&b, "compression = %t\n", true)
		}
	}

	// --- filter ---
	if len(spec.Filter.KeyPrefixes) > 0 || len(spec.Filter.DBBlacklist) > 0 {
		b.WriteString("\n[filter]\n")
		if len(spec.Filter.KeyPrefixes) > 0 {
			fmt.Fprintf(&b, "key_prefixes = [\n")
			for _, p := range spec.Filter.KeyPrefixes {
				fmt.Fprintf(&b, "  %q,\n", p)
			}
			fmt.Fprintf(&b, "]\n")
		}
		if len(spec.Filter.DBBlacklist) > 0 {
			fmt.Fprintf(&b, "db_blacklist = [\n")
			for _, d := range spec.Filter.DBBlacklist {
				fmt.Fprintf(&b, "  %q,\n", d)
			}
			fmt.Fprintf(&b, "]\n")
		}
	}

	// --- advanced ---
	b.WriteString("\n[advanced]\n")
	if spec.Advanced.LogLevel != "" {
		fmt.Fprintf(&b, "log_level = %q\n", spec.Advanced.LogLevel)
	}
	if spec.Advanced.LogFile != "" {
		fmt.Fprintf(&b, "log_file = %q\n", spec.Advanced.LogFile)
	}
	if spec.Advanced.Metrics {
		fmt.Fprintf(&b, "metric = %t\n", true)
	}
	if spec.Advanced.Pprof {
		fmt.Fprintf(&b, "pprof = %t\n", true)
	}
	if spec.Advanced.RestoreParallel > 0 {
		fmt.Fprintf(&b, "restore_parallel = %d\n", spec.Advanced.RestoreParallel)
	}

	// Final redactor pass: belt-and-braces in case a future caller
	// forgets to scrub a value. This is a defensive layer on top of
	// the env-var password handling above.
	out := redact.Redact(b.String())

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), 0o600)
}

// ArgvForShake returns the argv passed to the redis-shake binary. v1
// only uses the --conf flag; future shake versions may need extra
// flags here (e.g. --http-port) and that edit is one line.
func ArgvForShake(confPath string) []string {
	return []string{confPath}
}
