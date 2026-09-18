// Package agent — Registry: in-memory map of ToolKind → Driver.
//
// Driver registration is a code fact, not a configuration fact: tools
// are wired in at process init via init() in this package's child
// packages (e.g. internal/agent/redis). We deliberately do NOT persist
// the driver set in the database — adding a tool is a code change that
// ships with the binary, so a stale DB row would only confuse callers.
package agent

import (
	"fmt"
	"sync"
)

// Registry holds the set of Drivers wired into this clonectl build.
type Registry struct {
	mu      sync.RWMutex
	drivers map[ToolKind]Driver
}

// NewRegistry returns an empty Registry. Use Register to populate it.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[ToolKind]Driver)}
}

// Register installs d.Kind() → d. Panics on duplicate kind — this is a
// programming error caught at startup, not a runtime concern.
func (r *Registry) Register(d Driver) {
	if d == nil {
		panic("agent: nil Driver")
	}
	k := d.Kind()
	if !k.IsValid() {
		panic(fmt.Sprintf("agent: driver %T returned invalid ToolKind %q", d, k))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.drivers[k]; exists {
		panic(fmt.Sprintf("agent: duplicate driver for ToolKind %q", k))
	}
	r.drivers[k] = d
}

// Get returns the Driver registered for kind, or nil if none. Callers
// should treat nil as an unsupported-tool error.
func (r *Registry) Get(kind ToolKind) Driver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.drivers[kind]
}

// Kinds returns the registered kinds in arbitrary order. Useful for
// /v1/binary-info style introspection.
func (r *Registry) Kinds() []ToolKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolKind, 0, len(r.drivers))
	for k := range r.drivers {
		out = append(out, k)
	}
	return out
}

// Count reports how many drivers are registered. Helpful in tests.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.drivers)
}
