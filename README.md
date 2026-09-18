# clonectl

A multi-tool sync & verify control plane. Originally a thin Go wrapper
around `rclone rcd`, `clonectl` now manages **rclone** and any number of
**remote agents** (RedisShake / RedisFullCheck today; MongoShake /
KafkaShake / ...) through a single Driver contract.

```
┌────────────────────────────────────────────────────────────┐
│ clonectl (control plane)                                   │
│                                                            │
│   scheduler (gocron + leader lease)                        │
│   runner     (multi-tool dispatch via Driver contract)     │
│   alertmanager webhook (tool_kind label)                  │
│   embedded React UI                                        │
└─────────────────┬──────────────────────┬────────────────────┘
                  │ rcd RC API           │ HTTP + X-Agent-Token
                  ▼                      ▼
       ┌──────────────────┐    ┌─────────────────────────┐
       │ rclone rcd       │    │ redis-shake-agent        │
       │ (process-local)  │    │ (data plane, on Redis    │
       │                  │    │  host; pure black-box    │
       │                  │    │  exec)                   │
       └──────────────────┘    └────────────┬────────────┘
                                            │ exec
                                            ▼
                                   redis-shake / redis-full-check
                                   (upstream official binaries)
```

## Why a control plane?

A control plane keeps the boring parts — scheduling, RBAC, audit,
leader election, alerting, UI — in one place. Each upstream tool's
quirks (TOML shape, argv flags, progress format, topology changes)
live in **one process** (the agent) so they don't ripple back into
the control plane. When RedisShake v5 ships a new reader mode, the
change is:

1. Replace the upstream binary on the agent host.
2. Bump `upstream_version_pinned` in the agent YAML.
3. If the TOML schema changed, edit `cmd/redis-shake-agent/internal/runner/tomlgen.go`.

`clonectl` does **not** ship a new release for any of this.

## Features

- **Storage Sources**: S3-compatible object storage, local filesystem, Redis (standalone / cluster / sentinel / proxy).
- **Data Sources**: per-tenant binding to a Storage Source + per-DSN credentials. AK/SK for S3; password for Redis; nothing for local.
- **Sync Tasks**: cron-driven, per-tool dispatch via the Driver contract. rclone supports sync/copy; redis-shake supports sync_reader / rdb_reader / scan_reader.
- **Check Tasks**: rclone operations/check; redis-fullcheck supports compare modes 1 (full) / 2 (length) / 3 (existence) / 4 (adaptive).
- **Verify probes**: type-aware. rclone does List + WriteProbe (writes a tiny file and cleans it up); redis does PING + AUTH + INFO server inline.
- **Live progress**: 5s refresh on running tasks. Each Driver normalizes upstream-private counters into a single UI shape.
- **Alerts**: Alertmanager v4 webhook with `tool_kind` label so operators can route by upstream.
- **HA**: shared DB + leader_lease CAS election. Backups return 503 on writes.
- **Auth**: JWT + three roles (admin / edit / view) + bootstrap admin.
- **Embedded UI**: `go:embed web/dist`; single-binary deployment.

## Quick Start

### 1. Build

```bash
make all          # npm build frontend + go build (single ./clonectl binary)
make build-agent  # also build ./bin/redis-shake-agent
```

### 2. Configure

`cp .env.example .env` then edit:

```bash
# rclone (still the default tool)
RCLONE_RC_ADDR=0.0.0.0:5572
RCLONE_RC_USER=admin
RCLONE_RC_PASS=...

# Remote agent (RedisShake + RedisFullCheck)
AGENT_SHARED_TOKEN=...                # X-Agent-Token; empty disables
AGENT_DEFAULT_ENDPOINT=http://10.0.0.5:9010  # fallback agent
```

`redis-shake-agent` reads its own YAML — see `cmd/redis-shake-agent/README.md`.

### 3. Run

```bash
./clonectl serve    # control plane on :8000 (UI + API)
./bin/redis-shake-agent serve --config agent.yaml   # on the Redis host
```

### 4. Add a Redis Data Source

UI: Storage Sources → New → type=redis → topology=standalone → addresses=[10.0.0.1:6379] → Save.
Then Data Sources → New → Storage Source=[the redis one above] → password=<redis auth> → Save → Verify.

### 5. Add a Redis Sync Task

UI: Sync Tasks → New → pick the redis source and target DSNs → tool_kind=redis-shake → mode=sync_reader → cron → Save.

(For the create-form tool_kind selector + redis-mode sub-select to land in the UI, see the in-flight follow-up; the backend already accepts and round-trips those fields.)

## Adding a new tool (Driver)

`clonectl` is meant to be extensible. To add e.g. mongo-shake:

1. Create `cmd/mongo-shake-agent/` modeled on `cmd/redis-shake-agent/`.
2. Define `agent.ToolKind("mongo-shake")` in `internal/agent/driver.go`.
3. Implement `MongoShakeDriver` under `internal/agent/mongo/`, satisfying the existing `agent.Driver` interface.
4. Wire it in: `mongo.Register(a.Agents, agentHC)` in `internal/api/app.go`.
5. Add `case "mongo-shake":` in `internal/services/runner.go` (dispatch) and a runner file under `internal/services/runner_mongo.go`.

`clonectl` does not change. The Driver contract is the seam.

## Architecture docs

- `cmd/redis-shake-agent/README.md` — agent contract + upgrade flow.
- `docs/upgrade-friendliness.md` — why this design absorbs upstream drift cheaply.

## Testing

```bash
go test -race ./...                    # clonectl unit tests
make test-agent                       # agent unit tests
make build && make release            # cross-platform release tarballs
```
