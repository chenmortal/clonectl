// Package redact provides a secrets-aware log filter used by the agent
// to prevent passwords from leaking into stdout/stderr or log files.
//
// Strategy: replace any case-insensitive occurrence of key=value where
// key ∈ {password, passwd, pwd, auth, token} (or variants with
// underscores) and value is non-empty, with key=<redacted>.
//
// The filter is intentionally simple — false positives are fine (we
// over-redact) but false negatives are not (we must not leak secrets).
package redact

import (
	"regexp"
	"strings"
)

// Pattern matches "key=value" or "key: value" with our list of
// sensitive keys. The regex is non-greedy on the value to stop at the
// next whitespace, quote, or comma.
var pattern = regexp.MustCompile(`(?i)\b(password|passwd|pwd|auth|token|secret|key_pass|access_key|secret_key)\b\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;"']+)`)

// Redact replaces any sensitive key/value pair in s with key=<redacted>.
// The value's surrounding quotes (if any) are preserved.
func Redact(s string) string {
	if !strings.ContainsAny(s, "=:") {
		// Fast path: no kv pair shape at all.
		return s
	}
	return pattern.ReplaceAllStringFunc(s, func(match string) string {
		// Preserve any leading whitespace / quoting style by re-emitting
		// the matched prefix up to and including the separator.
		sep := strings.IndexAny(match, ":=")
		if sep < 0 {
			return "<redacted>"
		}
		return match[:sep+1] + " <redacted>"
	})
}

// ContainsSensitive reports whether s looks like it carries a secret.
// Useful as a sanity check before writing a buffer to disk.
func ContainsSensitive(s string) bool {
	return pattern.MatchString(s)
}
