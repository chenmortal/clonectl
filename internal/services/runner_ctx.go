// Package services — runner context.
//
// RunnerContext is the bundle of long-lived dependencies the runner
// needs: the rclone client (for rclone tasks) and the agent.Registry
// (for shake / fullcheck tasks). It is passed explicitly to RunTask /
// RunCheck so the signature stays stable as we add new tools.
//
// A nil Registry means "no agents wired" — RunTask then refuses to
// dispatch redis tasks with ErrAgentUnavailable instead of panicking,
// which is the behavior we want on a control-plane-only install.
package services

import (
	"clonectl/internal/agent"
	"clonectl/internal/rclone"
)

// RunnerContext bundles the per-process dependencies the runner
// needs. It is constructed once at app start and threaded through
// the scheduler / handler entry points.
type RunnerContext struct {
	// Rclone is the existing rcd HTTP client. Always non-nil in
	// production builds (rclone is still the default tool_kind).
	Rclone *rclone.Client

	// Agents is the registered agent drivers. May be nil when no
	// remote agents are configured; RunTask then falls back to
	// rclone-only paths and returns an explicit error on redis tasks.
	Agents *agent.Registry

	// CheckTimeout is the upstream-tool timeout in seconds, used by
	// the existing rclone PreCheck flow.
	CheckTimeout int

	// Locator picks which agent endpoint handles a given task based
	// on DataSource labels (agent_group / region). Optional — when
	// nil, tasks targeting a remote agent are rejected.
	Locator *agent.Locator
}
