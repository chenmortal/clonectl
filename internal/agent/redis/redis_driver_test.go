package redis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"clonectl/internal/agent"
)

// fakeAgent is a configurable httptest server that pretends to be a
// redis-shake-agent. It records each request so tests can assert on
// the wire shape, and returns canned responses.
type fakeAgent struct {
	srv      *httptest.Server
	submits  atomic.Int64
	stopHits atomic.Int64
	statuses atomic.Int64
}

func newFakeAgent() *fakeAgent {
	f := &fakeAgent{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"now":"now"}}`)
	})
	mux.HandleFunc("/v1/tasks/submit", func(w http.ResponseWriter, r *http.Request) {
		f.submits.Add(1)
		var got submitEnvelope
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		// Echo back: confirm we saw tool/task_id and that secrets were
		// passed in the envelope (not the URL).
		if got.Tool != "redis-shake" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"invalid_spec","message":"wrong tool"}}`)
			return
		}
		if got.TaskID == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"invalid_spec","message":"missing task_id"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"data":{"task_id":"`+got.TaskID+`","pid":9999,"started_at_unix_ms":1234567890,"state":"running"}}`)
	})
	mux.HandleFunc("/v1/tasks/stop", func(w http.ResponseWriter, r *http.Request) {
		f.stopHits.Add(1)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"task_id":"abc","state":"stopped"}}`)
	})
	mux.HandleFunc("/v1/tasks/status", func(w http.ResponseWriter, r *http.Request) {
		f.statuses.Add(1)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"task_id":"abc","tool":"redis-shake","mode":"sync_reader","state":"running","pid":9999,"exit_code":-1,"started_at_unix_ms":1234567890,"work_dir":"/work/abc","raw":{"payload":""}}}`)
	})
	mux.HandleFunc("/v1/tasks/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"tasks":[]}}`)
	})
	mux.HandleFunc("/v1/tasks/logs", func(w http.ResponseWriter, r *http.Request) {
		// content is base64 because agent.LogsChunk.Content is []byte.
		_, _ = io.WriteString(w, `{"ok":true,"data":{"task_id":"abc","offset":5,"content":"aGVsbG8=","eof":false}}`)
	})
	mux.HandleFunc("/v1/tasks/metrics", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"task_id":"abc","progress":{"total":100,"done":42,"percent":42,"throughput":1.5,"lag_seconds":0.2,"stage":"incremental"}}}`)
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeAgent) endpoint() string { return f.srv.URL }
func (f *fakeAgent) close()           { f.srv.Close() }

// --- ShakeDriver ---

func TestShakeDriver_Kind(t *testing.T) {
	d := NewShakeDriver(nil)
	if d.Kind() != agent.ToolRedisShake {
		t.Fatalf("Kind=%s, want redis-shake", d.Kind())
	}
}

func TestShakeDriver_ValidateSpec(t *testing.T) {
	d := NewShakeDriver(nil)
	cases := []struct {
		name string
		spec agent.Spec
		ok   bool
	}{
		{
			name: "sync_reader happy path",
			spec: agent.Spec{
				Mode: "sync_reader",
				Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
				Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
			},
			ok: true,
		},
		{
			name: "rdb_reader no target",
			spec: agent.Spec{
				Mode: "rdb_reader",
				Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
			},
			ok: true,
		},
		{
			name: "bad mode",
			spec: agent.Spec{Mode: "made_up"},
			ok: false,
		},
		{
			name: "missing addresses",
			spec: agent.Spec{
				Mode:   "sync_reader",
				Source: agent.Endpoint{Mode: ModeStandalone},
				Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
			},
			ok: false,
		},
		{
			name: "sentinel missing master_name",
			spec: agent.Spec{
				Mode: "sync_reader",
				Source: agent.Endpoint{Mode: ModeSentinel, Addresses: []string{"10.0.0.1:26379"}},
				Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
			},
			ok: false,
		},
		{
			name: "sentinel with master_name ok",
			spec: agent.Spec{
				Mode: "sync_reader",
				Source: agent.Endpoint{
					Mode: ModeSentinel, Addresses: []string{"10.0.0.1:26379", "10.0.0.2:26379"}, MasterName: "mymaster",
				},
				Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
			},
			ok: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.ValidateSpec(tc.spec)
			if tc.ok && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestShakeDriver_Submit(t *testing.T) {
	f := newFakeAgent()
	defer f.close()
	hc := agent.NewHTTPClient(agent.HTTPClientOptions{})
	d := NewShakeDriver(hc)
	spec := agent.Spec{
		Mode: "sync_reader",
		Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
		Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
	}
	out, err := d.Submit(context.Background(), f.endpoint(), agent.SubmitRequest{
		TaskID: "t1",
		Spec:   spec,
		Secrets: agent.Secrets{SourcePassword: "secret", TargetPassword: "secret2"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.TaskID != "t1" || out.PID != 9999 || out.State != "running" {
		t.Errorf("bad SubmitResponse: %+v", out)
	}
	if f.submits.Load() != 1 {
		t.Errorf("submits=%d, want 1", f.submits.Load())
	}
}

func TestShakeDriver_Stop_Status_Logs(t *testing.T) {
	f := newFakeAgent()
	defer f.close()
	hc := agent.NewHTTPClient(agent.HTTPClientOptions{})
	d := NewShakeDriver(hc)
	ctx := context.Background()

	if err := d.Stop(ctx, f.endpoint(), "t1", true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if f.stopHits.Load() != 1 {
		t.Errorf("stopHits=%d", f.stopHits.Load())
	}

	s, err := d.Status(ctx, f.endpoint(), "t1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if s.State != agent.StateRunning {
		t.Errorf("Status.State=%s, want running", s.State)
	}
	if f.statuses.Load() != 1 {
		t.Errorf("statuses=%d", f.statuses.Load())
	}

	chunk, err := d.Logs(ctx, f.endpoint(), "t1", agent.LogsOpts{Offset: 0, Limit: 64})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if chunk.Offset != 5 || string(chunk.Content) != "hello" {
		t.Errorf("Logs chunk=%+v", string(chunk.Content))
	}
}

func TestShakeDriver_TranslateProgress(t *testing.T) {
	d := NewShakeDriver(nil)
	payload := mustEncode(t, agentProgress{Total: 100, Done: 42, Throughput: 1.5, LagSeconds: 0.2, Stage: "incremental"})
	got := d.TranslateProgress(agent.RawProgress{Tool: agent.ToolRedisShake, Payload: payload})
	if got.Total != 100 || got.Done != 42 || got.Percent != 42 {
		t.Errorf("TranslateProgress=%+v", got)
	}
	if got.Stage != "incremental" {
		t.Errorf("Stage=%q", got.Stage)
	}
}

func TestShakeDriver_TranslateProgress_Overrun_Clamps(t *testing.T) {
	d := NewShakeDriver(nil)
	payload := mustEncode(t, agentProgress{Total: 100, Done: 250})
	got := d.TranslateProgress(agent.RawProgress{Tool: agent.ToolRedisShake, Payload: payload})
	if got.Percent != 100 {
		t.Errorf("Percent=%d, want 100", got.Percent)
	}
}

// --- FullCheckDriver ---

func TestFullCheckDriver_Kind(t *testing.T) {
	d := NewFullCheckDriver(nil)
	if d.Kind() != agent.ToolRedisFullCheck {
		t.Fatalf("Kind=%s, want redis-fullcheck", d.Kind())
	}
}

func TestFullCheckDriver_ValidateSpec(t *testing.T) {
	d := NewFullCheckDriver(nil)
	for _, mode := range []string{"1", "2", "3", "4"} {
		spec := agent.Spec{
			Mode: mode,
			Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
			Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
		}
		if err := d.ValidateSpec(spec); err != nil {
			t.Errorf("mode %s should validate: %v", mode, err)
		}
	}
	if err := d.ValidateSpec(agent.Spec{Mode: "9"}); err == nil {
		t.Errorf("mode 9 should reject")
	}
}

func TestFullCheckDriver_TranslateProgress(t *testing.T) {
	d := NewFullCheckDriver(nil)
	payload := mustEncode(t, agentProgress{Total: 500, Done: 250, Stage: "round_2"})
	got := d.TranslateProgress(agent.RawProgress{Tool: agent.ToolRedisFullCheck, Payload: payload})
	if got.Percent != 50 {
		t.Errorf("Percent=%d, want 50", got.Percent)
	}
	if got.Stage != "round_2" {
		t.Errorf("Stage=%q", got.Stage)
	}
}

// --- HTTPClient forwards unsupported_mode error ---

func TestShakeDriver_Submit_UnsupportedMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"unsupported_mode","message":"sync_reader removed"}}`)
	}))
	defer srv.Close()
	hc := agent.NewHTTPClient(agent.HTTPClientOptions{})
	d := NewShakeDriver(hc)
	spec := agent.Spec{
		Mode:   "sync_reader",
		Source: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.1:6379"}},
		Target: agent.Endpoint{Mode: ModeStandalone, Addresses: []string{"10.0.0.2:6379"}},
	}
	_, err := d.Submit(context.Background(), srv.URL, agent.SubmitRequest{
		TaskID: "t1", Mode: "sync_reader", Spec: spec,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !agent.IsUnsupported(err) {
		t.Fatalf("expected IsUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "sync_reader removed") {
		t.Errorf("error message lost: %v", err)
	}
}

// --- helpers ---

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
