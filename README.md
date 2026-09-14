# rclone-sync

基于 rclone rcd 的对象存储周期同步服务（**Go 实现**）：gin 提供 API 与 Web 控制台，[go-co-op/gocron](https://github.com/go-co-op/gocron) 按 cron 调度并驱动主备选举与执行监控，任务配置与执行结果持久化到数据库（SQLite/MySQL），生产发布时单二进制内嵌前端静态资源。

> 旧版（Python / FastAPI + APScheduler）实现已被本重写替代，历史见 git
> baseline 提交。旧数据库可用 `rclone-sync migrate-legacy` 一次性拷贝到新
> `*_v2` 表（ID 不变，旧表不动，可回滚）。

## 功能

- **存储源 / 数据源**：云厂商模板（s3 含 MinIO/AWS/阿里/腾讯、oss、cos、gcs、azureblob、b2、swift、local），数据源绑定用户与 read/write/admin 权限；`verify` 探针验证读写可用性
- **同步任务**：cron 调度 sync/copy，可绑定「同步前一致性检查」（一致跳过 / 差异继续 / 出错阻止）
- **检查任务**：rclone operations/check，独立 cron 或仅手动/pre-check
- **实时监控**：运行中任务 5s 刷新进度/速度/ETA；执行历史与统计
- **调度监控**（新增）：基于 gocron 的调度器面板 —— job 列表、下次执行预览、连续失败告警、手动运行（走与 trigger 相同的服务层）
- **告警**：失败触发 / 成功解除，Alertmanager v4 webhook（`notified_at` 去重）
- **主备 HA**：两节点共享 MySQL，DB lease CAS 选举；备机读开放、写返回 503
- **鉴权**：JWT + 三角色 RBAC（admin/edit/view）+ bootstrap admin
- **rclone 代理**：`/rclone/*` 反向代理（服务端注入 Basic Auth，读全角色 / 写 admin）
- **静态分发**：`go:embed` 内嵌 `web/dist`，单二进制部署；`STATIC_DIR` 可覆盖为磁盘目录（开发模式）

## 快速开始

### 1. 构建

```bash
make all          # npm build 前端 + go build（单二进制 ./rclone-sync）
# 或仅后端：make build
```

依赖：Go 1.26+、Node 22+（仅构建前端时需要）。

### 2. 配置

```bash
cp .env.example .env    # 按需修改
```

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DATABASE_URL` | `sqlite:///./rclone_sync.db` | MySQL 示例：`mysql://user:pass@host:3306/db`（兼容 `sqlite:///`、`mysql+pymysql://` 旧写法） |
| `RCLONE_RC_URL` | `http://localhost:5572` | rclone rcd 地址 |
| `RCLONE_RC_USER` / `RCLONE_RC_PASS` | `admin` / `6051` | RC API 认证 |
| `RCLONE_MANAGED` | `true` | `true` 时 `serve` 自动拉起/停止 rcd 子进程 |
| `RCLONE_BIN` / `RCLONE_RC_ADDR` | `rclone` / `0.0.0.0:5572` | rcd 二进制 / 监听地址 |
| `POLL_INTERVAL_SECONDS` | `10` | 运行中任务结果轮询间隔 |
| `CHECK_TIMEOUT_SECONDS` | `3600` | 同步前检查阻塞上限 |
| `API_HOST` / `API_PORT` | `0.0.0.0` / `8000` | API 监听地址 |
| `STATIC_DIR` | `web/dist` | 磁盘静态目录（优先于内嵌；开发热更新用） |
| `LOG_LEVEL` | `INFO` | DEBUG/WARN/… |
| `JWT_SECRET` | 占位符 | **生产必填** 32+ 随机字节（`openssl rand -hex 32`），HA 节点必须一致 |
| `JWT_EXPIRES_MINUTES` / `BCRYPT_ROUNDS` | `480` / `12` | token 有效期 / 密码哈希成本 |
| `BOOTSTRAP_ADMIN_USER` / `_PASSWORD` | 空 | users 表为空时自动创建 admin（幂等） |
| `NODE_ID` / `CLUSTER_NAME` | 自动 / `default` | HA 节点标识 / 集群名（两节点必须一致） |
| `HEARTBEAT_INTERVAL_SECONDS` / `LEASE_DURATION_SECONDS` | `3` / `10` | 心跳 / 租约（建议 ≥ 心跳×3） |
| `ALERTMANAGER_URL` | 空 | 告警 webhook 默认值（可在系统设置里改） |

### 3. 启动

```bash
./rclone-sync serve
# 浏览器访问 http://<host>:8000/ → 登录即用
```

`serve` 会自动建表、（`RCLONE_MANAGED=true` 时）拉起 rcd、幂等推送旧存储配置、
启动 gocron 调度器与选举，并 bootstrap 管理员。

## CLI

```text
rclone-sync serve            启动 API + gocron 调度器
rclone-sync stop             停止主服务与被管的 rclone rcd
rclone-sync run-once TASK_ID 立即执行指定任务一次（不经调度器）
rclone-sync status           检查 rcd / 服务 / 集群状态
rclone-sync migrate-legacy   旧 Python 表 → 新 *_v2 表（幂等，不改旧表）
```

## API 一览

完整接口文档见 [docs/api.md](docs/api.md)（以 Python 版接口为基线，字段与状态码保持兼容）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查（含 rcd 连通性与集群信息） |
| * | `/rclone/{path}` | 反向代理到 rclone rcd（注入认证；读全角色 / 写 admin） |
| POST/GET | `/api/auth/login` `/api/auth/me` `/api/auth/change-password` | 登录 / 当前用户 / 改密 |
| CRUD | `/api/users`（admin） | 用户管理、重置密码 |
| GET | `/api/storages` | 旧版存储只读（写操作 410 Gone） |
| CRUD | `/api/storage-sources` | 云厂商模板（写需 leader+admin） |
| CRUD | `/api/data-sources` | 数据源 + `/{id}/verify` + `/{id}/bindings` |
| CRUD | `/api/tasks` `/api/check-tasks` | 同步 / 检查任务 |
| POST | `/api/tasks/{id}/trigger` `/api/check-tasks/{id}/trigger` | 手动触发（202） |
| GET | `/api/runs` `/api/checks` | 执行历史（运行中附带 live 实时数据） |
| CRUD | `/api/system-settings`（admin） | 系统设置 + alertmanager 测试 |
| GET | `/api/scheduler/jobs[/{id}]` `/api/scheduler/overview` | **调度监控**（gocron 面板数据） |
| POST | `/api/scheduler/jobs/{id}/run` | 手动运行一个已注册 job（leader+edit） |

## 主备部署（HA）

两台机器共享一个 MySQL，`cluster_nodes` / `leader_lease` 两张表 + lease 续约
CAS 选举，无需 etcd/keepalived。代码：`internal/cluster`。

```bash
# 节点 A
NODE_ID=nodeA CLUSTER_NAME=rclone-sync DATABASE_URL='mysql://user:pass@db/rclone_sync' ./rclone-sync serve
# 节点 B（同一 DB）
NODE_ID=nodeB CLUSTER_NAME=rclone-sync DATABASE_URL='mysql://user:pass@db/rclone_sync' API_PORT=8001 ./rclone-sync serve
# 故障演练：kill nodeA，约 lease 周期后
curl :8001/healthz | jq '.is_leader'   # true
```

- **调度闸门**：只有 leader 注册 cron job；丢权节点即刻移除任务 job（运行中的自然结束）
- **写闸门**：配置类写操作在备机返回 `503 {"error":"not_leader",...}` + `Retry-After: 5`
- **触发/读**：两节点均可读；手动 trigger 不设闸门（与旧版一致）
- 备机接管时间 ≈ `LEASE_DURATION_SECONDS` + 选举周期（30s）
- 升级：业务表已改名（`*_v2`），新旧版本不能混跑业务数据；停机窗口内
  `migrate-legacy` + 两节点同时切换

## 开发

```bash
make test         # go test ./...
make vet fmt
npm --prefix web run dev   # vite :5173，代理 /api /rclone /healthz → :8000
```

## 已知取舍

- SQLite 作共享 HA 库仅测试可用（单写者串行）；生产 HA 必须 MySQL
- `/api/*` 未知路径返回 JSON 404（旧版回 index.html）；前端不依赖该行为
- 调度监控的执行历史在内存中，重启清零
