// Package agent — HTTPClient: thin transport shared by every Driver.
//
// Responsibilities:
//   - Dial the agent endpoint with a configurable timeout.
//   - Inject the X-Agent-Token header (v1 auth scheme).
//   - Decode the standard response envelope `{ok, data, error}`.
//   - Map common HTTP/transport failures to DriverError codes.
//
// Design notes:
//   - No retry by default: transient failures should be visible to the
//     scheduler so it can mark a run as `agent_unreachable` rather than
//     silently papering over a dead agent.
//   - Drivers MUST construct SubmitRequest/TaskStatus and pass it
//     verbatim — this layer never inspects tool-private payloads.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthToken is the v1 shared secret sent in the X-Agent-Token header.
// Empty disables the header (useful for LAN dev with mTLS-only auth).
type AuthToken string

// HeaderXAgentToken is the canonical header name. Exposed so tests can
// assert on it without copy-pasting the literal.
const HeaderXAgentToken = "X-Agent-Token"

// HTTPClientOptions configures the transport. Zero values fall back to
// sensible defaults (10s timeout, no auth, no retry).
type HTTPClientOptions struct {
	// Timeout applies to every individual HTTP request. Polling loops
	// should pass a per-call context with their own deadline; this is
	// the per-request ceiling.
	Timeout time.Duration

	// AuthToken is sent as the X-Agent-Token header on every request.
	AuthToken AuthToken

	// Transport is an optional custom *http.Transport (for tests). When
	// nil, a default transport with sensible pool sizes is created.
	Transport http.RoundTripper
}

// HTTPClient is the clonectl-side transport for talking to remote agents.
// It is safe for concurrent use across goroutines.
type HTTPClient struct {
	opts HTTPClientOptions
	hc   *http.Client
}

// NewHTTPClient builds an HTTPClient. Call once at startup and share
// across all Driver instances.
func NewHTTPClient(opts HTTPClientOptions) *HTTPClient {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	tr := opts.Transport
	if tr == nil {
		tr = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
		}
	}
	return &HTTPClient{
		opts: opts,
		hc: &http.Client{
			Timeout:   opts.Timeout,
			Transport: tr,
		},
	}
}

// envelope mirrors the agent-side response shape:
//
//	{ "ok": true,  "data": {...} }
//	{ "ok": false, "error": {"code": "...", "message": "..."} }
//
// Drivers never construct envelopes themselves — they call Do() and
// get back (raw data, error). The HTTP layer handles decode.
type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *envError       `json:"error,omitempty"`
}

type envError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Do executes request against endpoint+path and decodes the envelope.
// On success, data is the raw JSON of the response body; on failure,
// a *DriverError is returned with the agent's error code preserved.
//
// The body parameter is the JSON-encoded request payload (may be nil
// for GET / DELETE). For DELETE-with-body requests, set body non-nil.
func (c *HTTPClient) Do(ctx context.Context, method, endpoint, path string, body any, out any) error {
	if _, err := url.Parse(endpoint); err != nil {
		return NewDriverError(ErrInvalidSpec, fmt.Sprintf("invalid endpoint %q: %v", endpoint, err), err)
	}
	if endpoint == "" {
		return NewDriverError(ErrInvalidSpec, "empty endpoint", nil)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := strings.TrimRight(endpoint, "/") + path

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return NewDriverError(ErrInvalidSpec, fmt.Sprintf("marshal body: %v", err), err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return NewDriverError(ErrAgentUnreachable, fmt.Sprintf("build request: %v", err), err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.opts.AuthToken != "" {
		req.Header.Set(HeaderXAgentToken, string(c.opts.AuthToken))
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// Map the typical Go transport failures to a stable code so the
		// scheduler can branch on it without parsing strings.
		return NewDriverError(ErrAgentUnreachable, fmt.Sprintf("%s %s: %v", method, u, err), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return NewDriverError(ErrAgentUnreachable, fmt.Sprintf("read body: %v", err), err)
	}

	// HTTP status outside 2xx → surface as a DriverError so callers can
	// distinguish auth failure (401/403), not-found (404), etc.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := httpStatusToCode(resp.StatusCode)
		return NewDriverError(code, fmt.Sprintf("agent returned HTTP %d: %s", resp.StatusCode, truncate(string(raw), 256)), nil)
	}

	// 2xx but the body doesn't carry our envelope (e.g. /v1/tasks/logs
	// raw bytes) — try to decode anyway, fall back to leaving `out`
	// untouched if it stays nil and the body is non-JSON.
	var env envelope
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		// Non-JSON body. If the caller didn't ask for a parsed result,
		// surface the raw bytes via out as a json.RawMessage when possible.
		if out == nil {
			return nil
		}
		// Best-effort: copy the raw bytes into out if it's a RawMessage.
		if rm, ok := out.(*json.RawMessage); ok {
			*rm = json.RawMessage(raw)
			return nil
		}
		return NewDriverError(ErrInvalidSpec, fmt.Sprintf("decode envelope: %v; body=%s", jerr, truncate(string(raw), 128)), jerr)
	}

	if !env.OK && env.Error != nil {
		return NewDriverError(env.Error.Code, env.Error.Message, nil)
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return NewDriverError(ErrInvalidSpec, fmt.Sprintf("decode data: %v", err), err)
		}
	}
	return nil
}

// truncate clips s to max bytes for safe inclusion in error messages.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// httpStatusToCode maps well-known HTTP statuses to DriverError codes.
// Unknown statuses become a generic upstream_crash code.
func httpStatusToCode(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return ErrAuthFailed
	case http.StatusForbidden:
		return ErrAuthFailed
	case http.StatusNotFound:
		return ErrTaskNotFound
	case http.StatusBadRequest:
		return ErrInvalidSpec
	default:
		if status >= 500 {
			return ErrUpstreamCrash
		}
		return ErrUpstreamCrash
	}
}

// IsUnreachable reports whether err is an agent_unreachable failure.
// Drivers and the scheduler use this to branch on transient vs. fatal
// failures without unwrapping the concrete type.
func IsUnreachable(err error) bool {
	var de *DriverError
	if errors.As(err, &de) {
		return de.Code == ErrAgentUnreachable
	}
	return false
}

// IsUnsupported reports whether err is an unsupported_mode failure —
// the upstream tool no longer ships the requested mode. The scheduler
// can disable the task without spamming alerts.
func IsUnsupported(err error) bool {
	var de *DriverError
	if errors.As(err, &de) {
		return de.Code == ErrUnsupportedMode
	}
	return false
}
