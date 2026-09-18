# redis-shake-agent

A thin black-box executor for [tair-opensource/RedisShake](https://github.com/tair-opensource/RedisShake) and [tair-opensource/RedisFullCheck](https://github.com/tair-opensource/RedisFullCheck). `clonectl` calls this over HTTP to fork the upstream CLI on the host where Redis runs.

## Why a separate process?

clonectl owns the **control plane**: scheduling, RBAC, audit, leader election, alerting. The agent owns the **data plane**: actually running redis-shake / redis-full-check next to the source/target Redis. Splitting them lets each ship independently, lets operators place the agent on the Redis host (so traffic stays on-LAN), and lets upstream version drift be absorbed in one place — the agent — without touching clonectl.

## Contract

8 HTTP endpoints under `/v1`, all return the same envelope:

```
{"ok": true,  "data": {...}}
{"ok": false, "error": {"code": "...", "message": "..."}}
```

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/ping` | liveness probe |
| GET | `/v1/noop` | keepalive |
| POST | `/v1/tasks/submit` | start a new task; returns task_id, pid, started_at |
| GET | `/v1/tasks/list` | lightweight summaries |
| GET | `/v1/tasks/status` | full snapshot for one task |
| DELETE | `/v1/tasks/stop` | SIGTERM (graceful) → SIGKILL after grace |
| GET | `/v1/tasks/logs` | tail / range stdout (offset + limit) |
| GET | `/v1/tasks/metrics` | latest progress counters |
| GET | `/v1/version` | agent + pinned upstream versions |
| GET | `/v1/binary-info` | available modes / binary presence |

## Config

```yaml
bind_addr: "127.0.0.1:9010"
auth_token: "<shared-secret-or-empty>"
max_concurrent_tasks: 16
work_root: "/var/lib/redis-shake-agent/works"
log_root:  "/var/log/redis-shake-agent"
shutdown_grace_seconds: 30

binary_paths:
  redis-shake:     "/usr/local/bin/redis-shake"
  redis-fullcheck: "/usr/local/bin/redis-full-check"

upstream_version_pinned:
  redis-shake:     "v4.3.0"
  redis-fullcheck: "v1.4.0"
```

`auth_token` is sent as the `X-Agent-Token` header on every request. Empty disables auth (dev / LAN only). v2 will replace this with mTLS.

## Upstream upgrade flow

1. Stage the new binary at the path referenced by `binary_paths.<tool>`. Restart `redis-shake-agent`.
2. `/v1/version` now reports the new version under `upstream.<tool>`.
3. If the upstream TOML / argv changed in a breaking way, edit `internal/runner/tomlgen.go` (shake) or `internal/runner/argvgen.go` (fullcheck). clonectl does not need to change.
4. If a reader/compare mode was removed, the agent's `/v1/tasks/submit` returns `unsupported_mode` (HTTP 400). clonectl learns about it on its next Submit and marks the existing task `unsupported_mode` in the DB without crashing.
5. Roll back by repointing `binary_paths.<tool>` to the previous binary and restarting the agent.

## Build

The agent is a standalone Go module at `cmd/redis-shake-agent/go.mod`. Build it separately:

```bash
cd cmd/redis-shake-agent
go build -trimpath -o ../../bin/redis-shake-agent .
```

## Test

```bash
cd cmd/redis-shake-agent
go test -race ./...
```

The test suite uses a tiny shell-script fake in place of the upstream binaries so it runs on any Unix with `/bin/sh`.
