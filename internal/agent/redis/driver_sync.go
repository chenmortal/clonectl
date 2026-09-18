// Package redis — driver_sync.go: Driver for the redis-shake tool.
//
// Implements agent.Driver for ToolKind = ToolRedisShake. Talks to
// redis-shake-agent's /v1/tasks/* endpoints through a shared
// HTTPClient.
package redis

import (
	"context"

	"clonectl/internal/agent"
)

// ShakeDriver handles "redis-shake" tasks.
type ShakeDriver struct {
	hc *agent.HTTPClient
}

// NewShakeDriver wires a Driver around the shared HTTP transport.
// Pass the same HTTPClient to every Driver so the agent dial pool
// stays warm.
func NewShakeDriver(hc *agent.HTTPClient) *ShakeDriver {
	return &ShakeDriver{hc: hc}
}

// Kind implements agent.Driver.
func (d *ShakeDriver) Kind() agent.ToolKind { return agent.ToolRedisShake }

// ValidateSpec implements agent.Driver. Pure-local check; no network.
func (d *ShakeDriver) ValidateSpec(spec agent.Spec) error {
	return validateShakeSpec(spec)
}

// Submit implements agent.Driver.
func (d *ShakeDriver) Submit(ctx context.Context, endpoint string, req agent.SubmitRequest) (agent.SubmitResponse, error) {
	payload, err := specToShakePayload(req.Spec)
	if err != nil {
		return agent.SubmitResponse{}, agent.NewDriverError(agent.ErrInvalidSpec, err.Error(), err)
	}
	wire := submitEnvelope{
		Tool:   string(agent.ToolRedisShake),
		TaskID: req.TaskID,
		Mode:   req.Mode,
		Config: payload,
		Secrets: submitSecrets{
			SourcePassword: req.Secrets.SourcePassword,
			TargetPassword: req.Secrets.TargetPassword,
		},
	}
	var out agent.SubmitResponse
	if err := d.hc.Do(ctx, "POST", endpoint, "/v1/tasks/submit", wire, &out); err != nil {
		return agent.SubmitResponse{}, err
	}
	return out, nil
}

// Stop implements agent.Driver.
func (d *ShakeDriver) Stop(ctx context.Context, endpoint, taskID string, graceful bool) error {
	path := "/v1/tasks/stop?task_id=" + urlEncode(taskID) + "&graceful="
	if graceful {
		path += "true"
	} else {
		path += "false"
	}
	return d.hc.Do(ctx, "DELETE", endpoint, path, nil, nil)
}

// Status implements agent.Driver.
func (d *ShakeDriver) Status(ctx context.Context, endpoint, taskID string) (agent.TaskStatus, error) {
	var out agent.TaskStatus
	if err := d.hc.Do(ctx, "GET", endpoint, "/v1/tasks/status?task_id="+urlEncode(taskID), nil, &out); err != nil {
		return agent.TaskStatus{}, err
	}
	return out, nil
}

// Logs implements agent.Driver.
func (d *ShakeDriver) Logs(ctx context.Context, endpoint, taskID string, opts agent.LogsOpts) (agent.LogsChunk, error) {
	path := "/v1/tasks/logs?task_id=" + urlEncode(taskID)
	if opts.Offset != 0 {
		path += "&offset=" + itoa(opts.Offset)
	}
	if opts.Limit != 0 {
		path += "&limit=" + itoa(int64(opts.Limit))
	}
	var out agent.LogsChunk
	if err := d.hc.Do(ctx, "GET", endpoint, path, nil, &out); err != nil {
		return agent.LogsChunk{}, err
	}
	return out, nil
}

// List implements agent.Driver.
func (d *ShakeDriver) List(ctx context.Context, endpoint string, filter agent.ListFilter) ([]agent.TaskSummary, error) {
	path := "/v1/tasks/list?tool=" + string(agent.ToolRedisShake)
	if filter.Tool != "" {
		path = "/v1/tasks/list?tool=" + string(filter.Tool)
	}
	var out struct {
		Tasks []agent.TaskSummary `json:"tasks"`
	}
	if err := d.hc.Do(ctx, "GET", endpoint, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

// Metrics implements agent.Driver.
func (d *ShakeDriver) Metrics(ctx context.Context, endpoint, taskID string) (agent.MetricsSnapshot, error) {
	var out agent.MetricsSnapshot
	if err := d.hc.Do(ctx, "GET", endpoint, "/v1/tasks/metrics?task_id="+urlEncode(taskID), nil, &out); err != nil {
		return agent.MetricsSnapshot{}, err
	}
	return out, nil
}

// TranslateProgress implements agent.Driver.
//
// The agent returns its private Progress shape (Total/Done/Percent/
// LagSeconds/Stage) inside the metrics endpoint. We re-encode it as
// the UI-facing NormalizedProgress with percent clamping.
func (d *ShakeDriver) TranslateProgress(raw agent.RawProgress) agent.NormalizedProgress {
	var pr agentProgress
	if len(raw.Payload) > 0 {
		_ = jsonUnmarshal(raw.Payload, &pr)
	}
	return agent.NormalizedProgress{
		Total:      pr.Total,
		Done:       pr.Done,
		Percent:    clampPercent(pr.Done, pr.Total),
		Throughput: pr.Throughput,
		LagSeconds: pr.LagSeconds,
		Stage:      pr.Stage,
	}
}
