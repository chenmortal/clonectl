// Package redis — driver_fullcheck.go: Driver for the
// redis-fullcheck tool.
package redis

import (
	"context"

	"clonectl/internal/agent"
)

// FullCheckDriver handles "redis-fullcheck" tasks.
type FullCheckDriver struct {
	hc *agent.HTTPClient
}

// NewFullCheckDriver wires a Driver around the shared HTTP transport.
func NewFullCheckDriver(hc *agent.HTTPClient) *FullCheckDriver {
	return &FullCheckDriver{hc: hc}
}

// Kind implements agent.Driver.
func (d *FullCheckDriver) Kind() agent.ToolKind { return agent.ToolRedisFullCheck }

// ValidateSpec implements agent.Driver.
func (d *FullCheckDriver) ValidateSpec(spec agent.Spec) error {
	return validateFullCheckSpec(spec)
}

// Submit implements agent.Driver.
func (d *FullCheckDriver) Submit(ctx context.Context, endpoint string, req agent.SubmitRequest) (agent.SubmitResponse, error) {
	payload, err := specToFullCheckPayload(req.Spec)
	if err != nil {
		return agent.SubmitResponse{}, agent.NewDriverError(agent.ErrInvalidSpec, err.Error(), err)
	}
	wire := submitEnvelope{
		Tool:   string(agent.ToolRedisFullCheck),
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
func (d *FullCheckDriver) Stop(ctx context.Context, endpoint, taskID string, graceful bool) error {
	path := "/v1/tasks/stop?task_id=" + urlEncode(taskID) + "&graceful="
	if graceful {
		path += "true"
	} else {
		path += "false"
	}
	return d.hc.Do(ctx, "DELETE", endpoint, path, nil, nil)
}

// Status implements agent.Driver.
func (d *FullCheckDriver) Status(ctx context.Context, endpoint, taskID string) (agent.TaskStatus, error) {
	var out agent.TaskStatus
	if err := d.hc.Do(ctx, "GET", endpoint, "/v1/tasks/status?task_id="+urlEncode(taskID), nil, &out); err != nil {
		return agent.TaskStatus{}, err
	}
	return out, nil
}

// Logs implements agent.Driver.
func (d *FullCheckDriver) Logs(ctx context.Context, endpoint, taskID string, opts agent.LogsOpts) (agent.LogsChunk, error) {
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
func (d *FullCheckDriver) List(ctx context.Context, endpoint string, filter agent.ListFilter) ([]agent.TaskSummary, error) {
	path := "/v1/tasks/list?tool=" + string(agent.ToolRedisFullCheck)
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
func (d *FullCheckDriver) Metrics(ctx context.Context, endpoint, taskID string) (agent.MetricsSnapshot, error) {
	var out agent.MetricsSnapshot
	if err := d.hc.Do(ctx, "GET", endpoint, "/v1/tasks/metrics?task_id="+urlEncode(taskID), nil, &out); err != nil {
		return agent.MetricsSnapshot{}, err
	}
	return out, nil
}

// TranslateProgress implements agent.Driver. The agent reports
// round_N / finished stages; we surface the latest stage verbatim.
func (d *FullCheckDriver) TranslateProgress(raw agent.RawProgress) agent.NormalizedProgress {
	var pr agentProgress
	if len(raw.Payload) > 0 {
		_ = jsonUnmarshal(raw.Payload, &pr)
	}
	return agent.NormalizedProgress{
		Total:   pr.Total,
		Done:    pr.Done,
		Percent: clampPercent(pr.Done, pr.Total),
		Stage:   pr.Stage,
	}
}
