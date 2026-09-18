// Package runner — config projections used by the server layer.
//
// The server does not import internal/config directly because we
// want the runner to remain independent of the agent's YAML schema
// (which may evolve across releases). Instead we expose small typed
// projections (AgentConfig, ShakeConfig, FullCheckConfig) that the
// server can plug into.
//
// Adding a new tool = add a new Config struct here + a new
// ToolConfig field on AgentConfig.
package runner

import "errors"

// AgentConfig is the agent-wide runtime config used by handlers.
// Hydrated from the YAML Settings at startup.
type AgentConfig struct {
	BindAddr              string
	AuthToken             string
	Shake                 ShakeConfig
	FullCheck             FullCheckConfig
	LogRetentionDays      int
	UpstreamVersionPinned map[string]string
	HttpAPIExtras         map[string]HttpAPIExtras
}

// HttpAPIExtras mirrors config.HttpAPIExtra (kept here so server does
// not import config).
type HttpAPIExtras = struct {
	Binary    string   `yaml:"binary"`
	PortBase  int      `yaml:"port_base"`
	ExtraArgs []string `yaml:"extra_args"`
}

// ShakeSecrets carries the source/target passwords for a Shake
// submit. Defined as a value type so the handler can construct it
// without alias gymnastics.
type ShakeSecrets struct {
	SourcePassword string
	TargetPassword string
}

// FullCheckSecrets mirrors ShakeSecrets for the verification tool.
type FullCheckSecrets struct {
	SourcePassword string
	TargetPassword string
}

// DriverError is the agent-side error type returned from runner
// functions. The server maps it to the standard envelope code.
type DriverError struct {
	Code    string
	Message string
	Cause   error
}

// Error implements error.
func (e *DriverError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

// Unwrap exposes the underlying cause.
func (e *DriverError) Unwrap() error { return e.Cause }

// AsDriverError wraps an arbitrary error in a DriverError so the
// server can map it to the envelope. Nil errors pass through.
func AsDriverError(err error) error {
	if err == nil {
		return nil
	}
	var de *DriverError
	if errors.As(err, &de) {
		return err
	}
	return &DriverError{Code: "upstream_crash", Message: err.Error(), Cause: err}
}
