// Package redis implements the clonectl-side Driver that talks to a
// remote redis-shake-agent.
//
// Each Driver instance is bound to one ToolKind (ShakeDriver for
// "redis-shake" or FullCheckDriver for "redis-fullcheck"). The shared
// HTTPClient is the only network surface — Drivers translate the
// tool-agnostic Spec into the agent's wire shape and back.
package redis

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"clonectl/internal/agent"
)

// Mode sets the topology discriminator for an Endpoint.
const (
	ModeStandalone = "standalone"
	ModeCluster    = "cluster"
	ModeSentinel   = "sentinel"
	ModeProxy      = "proxy"
)

// validModes is the allow-list used by ValidateSpec across both
// drivers. Adding a topology = adding an entry here + an entry in
// the agent's spec.go parser.
var validModes = map[string]bool{
	ModeStandalone: true,
	ModeCluster:    true,
	ModeSentinel:   true,
	ModeProxy:      true,
}

// validateEndpoint checks the cross-field rules for a single Endpoint.
// Returns nil on success; an error suitable for the agent envelope
// (code: invalid_spec) on failure.
func validateEndpoint(prefix string, ep agent.Endpoint) error {
	if !validModes[ep.Mode] {
		return fmt.Errorf("%s: invalid topology %q (allowed: standalone/cluster/sentinel/proxy)", prefix, ep.Mode)
	}
	if len(ep.Addresses) == 0 {
		return fmt.Errorf("%s: at least one address required", prefix)
	}
	for i, a := range ep.Addresses {
		if a == "" {
			return fmt.Errorf("%s: addresses[%d] is empty", prefix, i)
		}
		// host:port sanity: split on last ':' and ensure both halves
		// are non-empty. We do NOT try to resolve DNS — the agent does
		// that at exec time.
		if _, _, err := splitHostPort(a); err != nil {
			return fmt.Errorf("%s: addresses[%d] %q: %w", prefix, i, a, err)
		}
	}
	if ep.Mode == ModeSentinel && ep.MasterName == "" {
		return fmt.Errorf("%s: sentinel topology requires master_name", prefix)
	}
	return nil
}

// splitHostPort parses "host:port" with the same leniency as net.SplitHostPort
// but accepts bracketed IPv6 hosts.
func splitHostPort(s string) (string, string, error) {
	// Treat as a URL-like host:port. url.Parse handles bracketed IPv6.
	u, err := url.Parse("//" + s)
	if err != nil {
		return "", "", err
	}
	if u.Hostname() == "" {
		return "", "", errors.New("missing host")
	}
	if u.Port() == "" {
		return "", "", errors.New("missing port")
	}
	return u.Hostname(), u.Port(), nil
}

// validateShakeSpec checks the Sync-spec rules shared by every Shake
// task. It does NOT verify the existence of source / target Redis —
// that's the agent's job at submit time.
func validateShakeSpec(spec agent.Spec) error {
	if spec.Mode != "sync_reader" && spec.Mode != "rdb_reader" && spec.Mode != "scan_reader" {
		return fmt.Errorf("shake: mode %q not supported (allowed: sync_reader / rdb_reader / scan_reader)", spec.Mode)
	}
	if err := validateEndpoint("source", spec.Source); err != nil {
		return err
	}
	// rdb_reader can run with no target (it just dumps a file), but
	// sync_reader / scan_reader need one.
	if spec.Mode != "rdb_reader" {
		if spec.Target.Mode == "" && spec.Target.Addresses == nil {
			return errors.New("shake: target required for this mode")
		}
		if err := validateEndpoint("target", spec.Target); err != nil {
			return err
		}
	}
	return nil
}

// validateFullCheckSpec checks the Verification-spec rules.
func validateFullCheckSpec(spec agent.Spec) error {
	switch spec.Mode {
	case "1", "2", "3", "4":
	default:
		return fmt.Errorf("fullcheck: compare_mode %q out of range [1..4]", spec.Mode)
	}
	if err := validateEndpoint("source", spec.Source); err != nil {
		return err
	}
	if err := validateEndpoint("target", spec.Target); err != nil {
		return err
	}
	return nil
}

// shakePayload is the agent-side wire shape under Config. It mirrors
// the agent's parseShakeSpec input.
type shakePayload struct {
	Source struct {
		Address  string `json:"address"`
		Username string `json:"username"`
	} `json:"source"`
	Target struct {
		Address     string `json:"address"`
		Username    string `json:"username"`
		Mode        string `json:"mode"`
		Parallel    int  `json:"parallel"`
		Compression bool `json:"compression"`
	} `json:"target"`
	Extra struct {
		Snapshot       bool   `json:"snapshot"`
		Parallel       int    `json:"parallel"`
		RDBPath        string `json:"rdb_path"`
		ScanCount      int    `json:"scan_count"`
		KeysPerRequest int64  `json:"keys_per_request"`
		Filter         struct {
			KeyPrefixes []string `json:"key_prefixes"`
			DBBlacklist []string `json:"db_blacklist"`
		} `json:"filter"`
		Advanced struct {
			LogLevel        string `json:"log_level"`
			Metrics         bool   `json:"metrics"`
			Pprof           bool   `json:"pprof"`
			RestoreParallel int    `json:"restore_parallel"`
		} `json:"advanced"`
	} `json:"extra"`
}

// fullCheckPayload is the agent-side wire shape under Config.
type fullCheckPayload struct {
	Source struct {
		Address string `json:"address"`
	} `json:"source"`
	Target struct {
		Address string `json:"address"`
	} `json:"target"`
	CompareTimes     int    `json:"compare_times"`
	QPS              int    `json:"qps"`
	IntervalSeconds  int    `json:"interval_seconds"`
	BatchCount       int    `json:"batch_count"`
	Parallel         int    `json:"parallel"`
	BigKeyThreshold  int64  `json:"big_key_threshold"`
	FilterList       string `json:"filter_list"`
}

// specToShakePayload renders the tool-agnostic Spec into the JSON the
// agent's parseShakeSpec understands. Unknown Extra blobs are passed
// through verbatim so future tool features can be wired without a
// driver code change.
func specToShakePayload(spec agent.Spec) (json.RawMessage, error) {
	var p shakePayload
	p.Source.Address = primaryAddress(spec.Source)
	p.Source.Username = spec.Source.Username
	p.Target.Address = primaryAddress(spec.Target)
	p.Target.Username = spec.Target.Username
	p.Target.Mode = "redis_writer"
	p.Target.Parallel = 0
	p.Target.Compression = false

	// Allow the caller to override Shake-only knobs via Extra without
	// the Driver needing to know about each one.
	if len(spec.Extra) > 0 {
		var extra shakePayload
		if err := json.Unmarshal(spec.Extra, &extra); err != nil {
			return nil, fmt.Errorf("decode shake extra: %w", err)
		}
		p.Target = extra.Target
		p.Extra.Snapshot = extra.Extra.Snapshot
		p.Extra.Parallel = extra.Extra.Parallel
		p.Extra.RDBPath = extra.Extra.RDBPath
		p.Extra.ScanCount = extra.Extra.ScanCount
		p.Extra.KeysPerRequest = extra.Extra.KeysPerRequest
		p.Extra.Filter = extra.Extra.Filter
		p.Extra.Advanced = extra.Extra.Advanced
	}

	if err := validateShakeSpec(spec); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

// specToFullCheckPayload renders Spec into the FullCheck payload.
func specToFullCheckPayload(spec agent.Spec) (json.RawMessage, error) {
	if err := validateFullCheckSpec(spec); err != nil {
		return nil, err
	}
	var p fullCheckPayload
	p.Source.Address = primaryAddress(spec.Source)
	p.Target.Address = primaryAddress(spec.Target)

	if len(spec.Extra) > 0 {
		var extra fullCheckPayload
		if err := json.Unmarshal(spec.Extra, &extra); err != nil {
			return nil, fmt.Errorf("decode fullcheck extra: %w", err)
		}
		p.CompareTimes = extra.CompareTimes
		p.QPS = extra.QPS
		p.IntervalSeconds = extra.IntervalSeconds
		p.BatchCount = extra.BatchCount
		p.Parallel = extra.Parallel
		p.BigKeyThreshold = extra.BigKeyThreshold
		p.FilterList = extra.FilterList
	}
	return json.Marshal(p)
}

// primaryAddress picks the first address from an Endpoint. Used to
// populate the single-string `address` field on the agent payload;
// the agent itself re-reads the cluster/sentinel seed list from
// addresses (it builds the full topology there).
func primaryAddress(ep agent.Endpoint) string {
	if len(ep.Addresses) == 0 {
		return ""
	}
	return strings.TrimSpace(ep.Addresses[0])
}
