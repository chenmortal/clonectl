// Package redis — register: plug both Redis drivers into a clonectl
// Registry.
//
// Callers (typically internal/api/app.go) invoke Register(reg, hc)
// once at startup. The Registry then exposes the drivers under
// ToolRedisShake and ToolRedisFullCheck without further wiring.
package redis

import (
	"clonectl/internal/agent"
)

// Register wires ShakeDriver + FullCheckDriver into reg using the
// supplied HTTPClient. Safe to call exactly once per process.
func Register(reg *agent.Registry, hc *agent.HTTPClient) {
	if reg == nil || hc == nil {
		return
	}
	reg.Register(NewShakeDriver(hc))
	reg.Register(NewFullCheckDriver(hc))
}
