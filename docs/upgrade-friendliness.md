# Upgrade Friendliness

How `clonectl` absorbs upstream version drift cheaply — and why the
agent black-box model is the right trade-off.

## The problem

Sync / verify tools evolve. Upstream RedisShake has renamed reader
modes, swapped TOML keys, and broken argv flags between minor
releases. If `clonectl` imported those tools as Go libraries, every
upstream bump would be a `clonectl` release:

- bump `go-redis` → resolve transitive deps → run full test matrix →
  ship a new tarball → coordinate deployment of control plane +
  every Redis host.

Worse, importing upstream code couples `clonectl` to upstream's Go
version, build tags, and import graph. A `go mod tidy` churn on the
upstream side becomes a forced release on our side.

## The agent model

Instead of importing upstream, we ship a thin **agent** binary that
fork/execs the upstream tool. `clonectl` talks to the agent over
HTTP. The agent's only responsibilities are:

1. Generate the upstream tool's config (TOML or argv) from a
   tool-agnostic JSON spec sent by `clonectl`.
2. Launch the upstream process.
3. Capture stdout / stderr / progress; surface a normalized status
   over HTTP.

`clonectl` never sees the upstream's TOML, argv, or protocol —
**only the agent's wire shape** which we control.

## The seam: the Driver interface

`internal/agent/driver.go` defines `Driver`. The implementation in
`internal/agent/redis/` talks to redis-shake-agent's `/v1/*` HTTP
endpoints. That contract has **8 methods**, **0 references** to
upstream's TOML or argv.

Adding a new upstream tool means writing **one Driver package** that
talks to a new agent — `clonectl` does not change. Adding a new
agent itself is just a new binary in `cmd/`.

## What stays clonectl's problem

- Authentication (X-Agent-Token).
- Connection-pooling, timeouts, envelope decoding.
- DriverError code mapping (`unsupported_mode`, `agent_unreachable`,
  `invalid_spec`, `auth_failed`, `upstream_crash`).
- In-memory task table, sweep, metrics.

These are general-purpose concerns. They don't move when upstream
moves.

## What the agent's problem is

- TOML schema drift.
- Argv flag rename.
- New reader / compare modes.
- Progress format changes (stdout regex, /metrics shape).

When any of these happen:

1. **Upstream renames a TOML key.** Edit
   `cmd/redis-shake-agent/internal/runner/tomlgen.go`. Ship a new
   agent. `clonectl` does not change.
2. **Upstream renames a flag.** Edit `argvgen.go`. Ship a new agent.
3. **Upstream releases a new version.** Replace the upstream binary
   on the agent host. Restart the agent. Bump
   `upstream_version_pinned` in the YAML. Done.
4. **Upstream removes a reader mode.** The agent's `Submit` returns
   `unsupported_mode` (HTTP 400). `clonectl` learns on its next
   Submit call and marks the existing task `unsupported_mode` in
   the DB without crashing. Operators see the error in the run row
   and can either delete the task or wait for upstream to bring it
   back.

## What stays upstream's problem

- Bugs in the upstream tool itself.
- Cluster topology change handling.
- Memory leaks in the upstream binary.

None of these move the agent or `clonectl`.

## The hard rule

> **The agent never imports upstream Go code.**

We could have saved bytes by linking `github.com/tair-opensource/RedisShake`
into the agent. We don't, on purpose. The upstream binary is the
boundary; everything inside is upstream's job.

If you find yourself wanting to add an upstream import to the
agent — for convenience, for type reuse, for testing — first ask:
**what does the wire shape look like?** Then add the call to the
agent's process manager, not its source code.

## Why pull, not push

`clonectl` polls the agent (every 5s by default). The alternative is
a webhook / SSE / WebSocket push from the agent to `clonectl`. Pull
is strictly simpler for our topology:

- Agents may sit behind NAT; `clonectl` always has a stable address.
- No inbound firewall rules on Redis hosts.
- Polling interval is configurable per deployment; push requires
  agent-side back-off logic that varies by upstream tool's
  progress granularity.

The cost is 5s of staleness — acceptable for sync / verify, where
the upstream tools themselves emit progress every 1–2 seconds and
the agent round-trips that to Prometheus.
