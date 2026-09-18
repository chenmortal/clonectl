package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubDriver is a minimal Driver for Registry tests.
type stubDriver struct {
	kind ToolKind
}

func (s *stubDriver) Kind() ToolKind                                       { return s.kind }
func (s *stubDriver) ValidateSpec(_ Spec) error                            { return nil }
func (s *stubDriver) Submit(_ context.Context, _ string, _ SubmitRequest) (SubmitResponse, error) {
	return SubmitResponse{}, nil
}
func (s *stubDriver) Stop(_ context.Context, _, _ string, _ bool) error { return nil }
func (s *stubDriver) Status(_ context.Context, _, _ string) (TaskStatus, error) {
	return TaskStatus{}, nil
}
func (s *stubDriver) Logs(_ context.Context, _, _ string, _ LogsOpts) (LogsChunk, error) {
	return LogsChunk{}, nil
}
func (s *stubDriver) List(_ context.Context, _ string, _ ListFilter) ([]TaskSummary, error) {
	return nil, nil
}
func (s *stubDriver) Metrics(_ context.Context, _, _ string) (MetricsSnapshot, error) {
	return MetricsSnapshot{}, nil
}
func (s *stubDriver) TranslateProgress(_ RawProgress) NormalizedProgress { return NormalizedProgress{} }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubDriver{kind: ToolRedisShake})
	r.Register(&stubDriver{kind: ToolRedisFullCheck})

	if r.Count() != 2 {
		t.Fatalf("Count=%d, want 2", r.Count())
	}
	if r.Get(ToolRedisShake) == nil {
		t.Fatalf("Get(redis-shake) returned nil")
	}
	if r.Get("not-a-tool") != nil {
		t.Fatalf("Get(unknown) should return nil")
	}
}

func TestToolKind_IsValid(t *testing.T) {
	for _, k := range []ToolKind{ToolRedisShake, ToolRedisFullCheck} {
		if !k.IsValid() {
			t.Errorf("%q should be valid", k)
		}
	}
	for _, k := range []ToolKind{"", "redis-mongo", "unknown"} {
		if k.IsValid() {
			t.Errorf("%q should be invalid", k)
		}
	}
}

func TestLocator_DefaultFallback(t *testing.T) {
	l := NewLocator(AgentEndpoint("http://10.0.0.5:9010"))
	got, err := l.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "http://10.0.0.5:9010" {
		t.Fatalf("Resolve=%s, want default", got)
	}
}

func TestLocator_NoDefaultNoMatch(t *testing.T) {
	l := NewLocator("")
	_, err := l.Resolve(map[string]string{"region": "cn-hangzhou"})
	if err == nil {
		t.Fatalf("expected error when no default and no rule matches")
	}
	var de *DriverError
	if !errors.As(err, &de) || de.Code != ErrInvalidSpec {
		t.Fatalf("expected ErrInvalidSpec, got %v", err)
	}
}

func TestLocator_RuleHit(t *testing.T) {
	l := NewLocator(AgentEndpoint("http://default:9010"),
		RoutingRule{Match: "region=cn-hangzhou", Endpoint: AgentEndpoint("http://hz:9010")},
		RoutingRule{Match: "region=ap-east-1", Endpoint: AgentEndpoint("http://ap:9010")},
	)
	cases := []struct {
		labels map[string]string
		want   AgentEndpoint
	}{
		{map[string]string{"region": "cn-hangzhou"}, "http://hz:9010"},
		{map[string]string{"region": "ap-east-1"}, "http://ap:9010"},
		{map[string]string{"region": "us-east-1"}, "http://default:9010"}, // fallback
		{nil, "http://default:9010"},                                       // fallback
	}
	for i, tc := range cases {
		got, err := l.Resolve(tc.labels)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got != tc.want {
			t.Errorf("case %d: got %s, want %s", i, got, tc.want)
		}
	}
}

func TestMatchSelector(t *testing.T) {
	cases := []struct {
		sel   string
		labels map[string]string
		want  bool
	}{
		{"", nil, true},
		{"", map[string]string{"a": "b"}, false},
		{"k=v", map[string]string{"k": "v"}, true},
		{"k=v", map[string]string{"k": "x"}, false},
		{"k=v", map[string]string{"other": "v"}, false},
		{"=v", map[string]string{"k": "v"}, false},
		{"k=", map[string]string{"k": ""}, false},
	}
	for i, tc := range cases {
		got := matchSelector(tc.sel, tc.labels)
		if got != tc.want {
			t.Errorf("case %d: matchSelector(%q,%v)=%v, want %v", i, tc.sel, tc.labels, got, tc.want)
		}
	}
}

func TestClampPercent(t *testing.T) {
	cases := []struct {
		done, total int64
		want        int
	}{
		{0, 0, -1},
		{0, -1, -1},
		{50, 100, 50},
		{100, 100, 100},
		{200, 100, 100},
		{0, 1, 0},
	}
	for i, tc := range cases {
		if got := ClampPercent(tc.done, tc.total); got != tc.want {
			t.Errorf("case %d: ClampPercent(%d,%d)=%d, want %d", i, tc.done, tc.total, got, tc.want)
		}
	}
}

func TestMergeLatest_NeverGoesBackwards(t *testing.T) {
	prev := DoneThroughput{Done: 100, Throughput: 1.5, LagSeconds: 0.5}
	// Next Done smaller → keep prev.
	next := MergeLatest(prev, DoneThroughput{Done: 80, Throughput: -1, LagSeconds: -1})
	if next.Done != 100 || next.Throughput != 1.5 || next.LagSeconds != 0.5 {
		t.Fatalf("MergeLatest regressed: %+v", next)
	}
	// Next Done larger → take next.
	next = MergeLatest(prev, DoneThroughput{Done: 200, Throughput: 2.0, LagSeconds: 0.1})
	if next.Done != 200 || next.Throughput != 2.0 || next.LagSeconds != 0.1 {
		t.Fatalf("MergeLatest should advance: %+v", next)
	}
}

// --- HTTPClient ---

func TestHTTPClient_OKEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderXAgentToken) != "secret" {
			t.Errorf("missing auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"data":{"task_id":"abc","pid":42,"started_at_unix_ms":1,"state":"running"}}`)
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientOptions{AuthToken: "secret"})
	var out SubmitResponse
	if err := c.Do(context.Background(), "POST", srv.URL, "/v1/tasks/submit", SubmitRequest{TaskID: "abc"}, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.TaskID != "abc" || out.PID != 42 || out.State != "running" {
		t.Fatalf("decoded wrong: %+v", out)
	}
}

func TestHTTPClient_ErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"unsupported_mode","message":"scan_reader removed in v4"}}`)
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientOptions{})
	err := c.Do(context.Background(), "POST", srv.URL, "/v1/tasks/submit", SubmitRequest{}, nil)
	var de *DriverError
	if !errors.As(err, &de) || de.Code != ErrUnsupportedMode {
		t.Fatalf("expected ErrUnsupportedMode, got %v", err)
	}
	if !IsUnsupported(err) {
		t.Fatalf("IsUnsupported should detect the error")
	}
}

func TestHTTPClient_HTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "task not found")
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientOptions{})
	err := c.Do(context.Background(), "GET", srv.URL, "/v1/tasks/status", nil, nil)
	var de *DriverError
	if !errors.As(err, &de) || de.Code != ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestHTTPClient_AuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientOptions{AuthToken: "wrong"})
	err := c.Do(context.Background(), "GET", srv.URL, "/v1/ping", nil, nil)
	var de *DriverError
	if !errors.As(err, &de) || de.Code != ErrAuthFailed {
		t.Fatalf("expected ErrAuthFailed, got %v", err)
	}
}

func TestHTTPClient_Unreachable(t *testing.T) {
	c := NewHTTPClient(HTTPClientOptions{Timeout: 1})
	// Use a closed port to trigger transport error.
	err := c.Do(context.Background(), "GET", "http://127.0.0.1:1", "/v1/ping", nil, nil)
	if err == nil {
		t.Fatalf("expected error for unreachable host")
	}
	if !IsUnreachable(err) {
		t.Fatalf("IsUnreachable should detect transport failure; got %v", err)
	}
}

func TestHTTPClient_RawJSONFallback(t *testing.T) {
	// Some endpoints (e.g. /v1/tasks/logs) may return raw bytes, not
	// the standard envelope. The client should still populate a
	// *json.RawMessage out parameter.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "not-json-content")
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientOptions{})
	var raw json.RawMessage
	if err := c.Do(context.Background(), "GET", srv.URL, "/v1/tasks/logs", nil, &raw); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(string(raw), "not-json-content") {
		t.Fatalf("raw body not propagated: %q", string(raw))
	}
}

func TestDriverError_Unwrap(t *testing.T) {
	base := errors.New("dial tcp: connection refused")
	de := NewDriverError(ErrAgentUnreachable, "ping failed", base)
	if !strings.Contains(de.Error(), "connection refused") {
		t.Errorf("Error() missing cause: %s", de.Error())
	}
	if !errors.Is(de, base) {
		t.Errorf("errors.Is should unwrap to base")
	}
}
