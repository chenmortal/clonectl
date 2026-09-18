// Package agent — Locator: pick the agent endpoint for a given task.
//
// Strategy: a task's data sources carry optional labels (agent_group,
// region) that map to a target endpoint. When no label is set, the
// default endpoint from config is used. This keeps the routing policy
// declarative and overridable per-deployment without code changes.
//
// The Locator is a thin stateless object — it holds the routing table
// built from config and is consulted on every Submit / Status / Stop.
//
// Naming note: this file uses AgentEndpoint to avoid colliding with the
// Spec.Endpoint struct (data-source connection shape) defined in
// driver.go. Both are "endpoint" concepts at different layers.
package agent

import (
	"fmt"
	"strings"
	"sync"
)

// AgentEndpoint is a network address for an agent (e.g.
// "http://10.0.0.5:9010"). v1: plain http://host:port string. Future:
// mTLS / unix socket / multi-endpoint HA pair.
type AgentEndpoint string

// String returns the address as a dial string.
func (e AgentEndpoint) String() string { return string(e) }

// RoutingRule maps a label selector to an endpoint. Multiple rules
// are tried in order; the first match wins.
type RoutingRule struct {
	// Match is a key=value selector (e.g. "agent_group=ap-east-1" or
	// "region=cn-hangzhou"). Empty Match means "fallback default".
	Match string
	// Endpoint is the agent address to use when Match hits.
	Endpoint AgentEndpoint
}

// Locator resolves (tool_kind, source_labels) → AgentEndpoint.
// Construct via NewLocator; mutate via AddRule / SetDefault.
type Locator struct {
	mu    sync.RWMutex
	rules []RoutingRule
	def   AgentEndpoint
}

// NewLocator builds a Locator with the supplied default endpoint and
// ordered rules. An empty default is allowed (returns "", forcing the
// caller to either configure a default or reject the task).
func NewLocator(def AgentEndpoint, rules ...RoutingRule) *Locator {
	return &Locator{rules: rules, def: def}
}

// SetDefault replaces the fallback endpoint.
func (l *Locator) SetDefault(def AgentEndpoint) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.def = def
}

// AddRule appends a routing rule. Rules are evaluated in insertion
// order; first match wins.
func (l *Locator) AddRule(rule RoutingRule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rules = append(l.rules, rule)
}

// Resolve returns the endpoint for the given labels. Labels may be nil.
// Resolution order:
//  1. If any rule's Match selector hits, return its endpoint.
//  2. Otherwise return the default endpoint (may be empty).
//
// Returns ErrInvalidSpec when no rule matches AND no default is set.
func (l *Locator) Resolve(labels map[string]string) (AgentEndpoint, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.rules) > 0 && labels != nil {
		for _, r := range l.rules {
			if matchSelector(r.Match, labels) {
				return r.Endpoint, nil
			}
		}
	}
	if l.def == "" {
		return "", NewDriverError(ErrInvalidSpec, "no agent endpoint configured and no routing rule matched", nil)
	}
	return l.def, nil
}

// matchSelector implements a tiny "k=v" matcher. Keys/values are
// case-sensitive. Empty selector matches only when labels is empty.
func matchSelector(selector string, labels map[string]string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return len(labels) == 0
	}
	eq := strings.IndexByte(selector, '=')
	if eq <= 0 || eq == len(selector)-1 {
		return false
	}
	k := strings.TrimSpace(selector[:eq])
	v := strings.TrimSpace(selector[eq+1:])
	got, ok := labels[k]
	return ok && got == v
}

// String renders the routing table for /v1/binary-info style dumps.
func (l *Locator) String() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var b strings.Builder
	fmt.Fprintf(&b, "default=%s rules=[", l.def)
	for i, r := range l.rules {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s->%s", r.Match, r.Endpoint)
	}
	b.WriteByte(']')
	return b.String()
}
