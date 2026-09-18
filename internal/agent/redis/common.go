// Package redis — common.go: wire shapes + tiny helpers shared by
// driver_sync.go and driver_fullcheck.go.
package redis

import (
	"encoding/json"
	"strconv"
)

// submitEnvelope is the request body for /v1/tasks/submit on the
// redis-shake-agent. It mirrors the agent's submitEnvelope type but
// uses the clonectl ToolKind enum directly.
type submitEnvelope struct {
	Tool    string          `json:"tool"`
	TaskID  string          `json:"task_id"`
	Mode    string          `json:"mode"`
	Config  json.RawMessage `json:"config"`
	Secrets submitSecrets   `json:"secrets"`
}

// submitSecrets carries the source/target passwords the agent needs
// to inject into the upstream tool's TOML/argv.
type submitSecrets struct {
	SourcePassword string `json:"source_password,omitempty"`
	TargetPassword string `json:"target_password,omitempty"`
}

// agentProgress mirrors the agent's Progress shape that ships in
// the metrics / status response. We re-declare it here so the Driver
// package does not need to import agent internals.
type agentProgress struct {
	Total      int64   `json:"total"`
	Done       int64   `json:"done"`
	Percent    int     `json:"percent"`
	Throughput float64 `json:"throughput"`
	LagSeconds float64 `json:"lag_seconds"`
	Stage      string  `json:"stage,omitempty"`
}

// clampPercent returns -1 when total is unknown, otherwise Done/Total
// clamped to [0, 100]. Lifted from agent package to avoid an import
// cycle (the agent package may import nothing under us).
func clampPercent(done, total int64) int {
	if total <= 0 {
		return -1
	}
	p := int((done * 100) / total)
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// urlEncode is a tiny stdlib-only URL escape (path-segment safe). The
// full net/url.QueryEscape is heavier than we need for task_id which
// is an opaque UUID-style string.
func urlEncode(s string) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '-' || c == '_' || c == '.' || c == '~':
			out = append(out, c)
		default:
			out = append(out, '%', hex[c>>4], hex[c&0xF])
		}
	}
	return string(out)
}

// itoa is a thin wrapper so drivers don't pull strconv directly into
// every line.
func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// jsonUnmarshal mirrors encoding/json's signature to keep call sites
// symmetric and let future error-folding sit in one place.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
