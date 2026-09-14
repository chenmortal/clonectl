# rclone-sync

基于 rclone rcd 的对象存储周期同步服务：FastAPI 提供管理 API，APScheduler 按 cron 调度，任务配置与执行结果持久化到数据库（SQLite/MySQL）。

## 快速启动

### 1. 环境准备（conda + Poetry）

```bash
conda create -n rclone python=3.10 -y
conda activate rclone

pip config set global.index-url https://mirrors.aliyun.com/pypi/simple
pip install poetry
poetry config virtualenvs.create false   # 复用 conda 环境，不重复建 venv
```

### 2. 安装依赖

```bash
poetry install          # 按 poetry.lock 精确安装，含 dev 组（pytest/ruff/mypy）
```

### 3. 配置

```bash
cp .env.example .env    # 按需修改数据库与 rclone rcd 连接信息
```

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DATABASE_URL` | `sqlite:///./rclone_sync.db` | MySQL 示例：`mysql+pymysql://user:pass@host/db` |
| `RCLONE_RC_URL` | `http://localhost:5572` | rclone rcd 地址 |
| `RCLONE_RC_USER` / `RCLONE_RC_PASS` | `admin` / `6051` | RC API 认证 |
| `RCLONE_MANAGED` | `true` | `true` 时 `serve` 自动拉起/停止 rcd 子进程；`false` 连接外部已运行的 rcd |
| `POLL_INTERVAL_SECONDS` | `10` | 任务结果轮询间隔 |
| `API_HOST` / `API_PORT` | `0.0.0.0` / `8000` | API 监听地址 |

### 4. 启动

```bash
poetry run rclone-sync serve       # 启动 API + 调度器（自动初始化数据库表）
```

> Poetry 通过 `[tool.poetry.scripts]` 将 Typer CLI 注册为 `rclone-sync` 命令；
> 也可用 `poetry shell` 进入环境后直接执行 `rclone-sync serve`，
> 或不装入口点直接 `poetry run python cli.py serve`。

### 5. 创建存储与任务（示例）

```bash
curl -X POST http://localhost:8000/api/storages -H "Content-Type: application/json" -d '{
  "name": "local-s3", "type": "s3",
  "parameters": {"provider": "Minio", "access_key_id": "rustfsadmin",
                 "secret_access_key": "rustfsadmin", "endpoint": "http://127.0.0.1:9000"}
}'

curl -X POST http://localhost:8000/api/storages -H "Content-Type: application/json" -d '{
  "name": "remote-s3", "type": "s3",
  "parameters": {"provider": "Minio", "access_key_id": "rustfsadmin",
                 "secret_access_key": "rustfsadmin", "endpoint": "http://10.126.126.1:9000"}
}'

curl -X POST http://localhost:8000/api/tasks -H "Content-Type: application/json" -d '{
  "name": "nightly-backup",
  "src_storage_id": 1, "src_path": "/data",
  "dst_storage_id": 2, "dst_path": "/data",
  "mode": "sync", "cron": "0 3 * * *", "enabled": true,
  "rclone_options": {"transfers": 4}
}'

curl -X POST http://localhost:8000/api/tasks/1/trigger   # 手动触发一次
curl http://localhost:8000/api/runs?task_id=1            # 查看执行结果
```

## CLI 命令参考（Typer）

所有命令均支持 `--help`；执行 `rclone-sync --install-completion` 可为当前 shell 安装命令与参数自动补全。

```text
Usage: rclone-sync [OPTIONS] COMMAND [ARGS]...

  rclone periodic sync service

╭─ Options ────────────────────────────────────────────────────────────────────╮
│ --install-completion          Install completion for the current shell.      │
│ --show-completion             Show completion for the current shell, to copy │
│                               it or customize the installation.              │
│ --help                        Show this message and exit.                    │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ Commands ───────────────────────────────────────────────────────────────────╮
│ serve      启动 FastAPI + APScheduler 服务（自动初始化数据库表）             │
│ stop       停止主服务与被管的 rclone rcd                                     │
│ run-once   立即执行指定任务一次                                              │
│ status     检查 rclone rcd 进程与主服务运行状态                              │
╰──────────────────────────────────────────────────────────────────────────────╯
```

### serve

```text
Usage: rclone-sync serve [OPTIONS]
```

启动 API 与调度器。启动时自动初始化数据库表（`create_all` 语义，仅创建不存在的表，**不会覆盖已有数据**），将数据库中全部存储配置幂等推送一次到 rclone rcd（应对 rcd 重启后配置丢失），并从数据库重建所有 enabled 任务的 cron 计划；默认（`RCLONE_MANAGED=true`）自动拉起 rclone rcd 子进程（含 `--rc-serve --rc-web-gui --rc-enable-metrics`，日志写入 `rcd.log`），等待就绪后才开始监听，服务退出时自动终止 rcd。主进程 PID 写入 `rclone-sync.pid`（可用 `PID_FILE` 配置），managed rcd PID 写入 `rcd.pid`。

### stop

```text
Usage: rclone-sync stop [OPTIONS]
```

停止服务：先向主进程发 SIGTERM（uvicorn 优雅退出，lifespan 自动停止被管的 rcd），超时 15s 后升级为 SIGKILL；随后兜底检查 `rcd.pid`，若 rcd 仍存活则一并终止并清理 PID 文件。

```bash
$ rclone-sync stop
stopping rclone-sync service (pid 89900)...
rclone-sync service: stopped
rclone rcd: not running
```

### run-once

```text
Usage: rclone-sync run-once [OPTIONS] TASK_ID

╭─ Arguments ──────────────────────────────────────────────────────────────────╮
│ *    task_id      <int>  [required]                                          │
╰──────────────────────────────────────────────────────────────────────────────╯
```

不经过调度器，立即执行指定任务一次（触发方式记为 manual），输出 run id 与 rclone jobid。

### status

```text
Usage: rclone-sync status [OPTIONS]
```

检查两个进程的运行状态并输出：

- **rclone rcd**：探测 RC API（`/core/version`），输出 running / stopped
- **rclone-sync service**：探测主服务 `/healthz`，输出 running / stopped 及 rcd 连通性

```bash
$ rclone-sync status
rclone rcd: running (http://localhost:5572)
rclone-sync service: running (http://127.0.0.1:8000/healthz, rclone_reachable=True)
```

> rclone rcd 进程本身的管理（拉起/停止）由 `server.sh` 或 `RCLONE_MANAGED=true` 时 `serve` 自动托管，CLI 不再提供单独的 rcd 子命令。

## 常用 Poetry 命令

```bash
poetry install                  # 按 lock 文件安装依赖
poetry add <pkg>                # 新增运行时依赖并更新 lock
poetry add --group dev <pkg>    # 新增开发依赖
poetry run <cmd>                # 在受管环境中执行命令
poetry shell                    # 激活受管环境
poetry env info                 # 查看当前环境信息
poetry update                   # 升级依赖并刷新 lock
```

## API 一览

完整对外接口文档见 [docs/api.md](docs/api.md)（字段定义、状态码、状态机、curl 示例）。

| 方法 | 路径 | 说明 |
|---|---|---|
| * | `/rclone/{path}` | 反向代理到 rclone rcd（服务端注入认证，见 docs/api.md §2） |
| GET | `/healthz` | 健康检查（含 rcd 连通性） |
| GET/POST | `/api/storages` | 存储配置列表 / 创建 |
| GET/PUT/DELETE | `/api/storages/{id}` | 存储配置详情 / 更新 / 删除 |
| GET/POST | `/api/tasks` | 任务列表 / 创建（自动注册调度） |
| GET/PUT/DELETE | `/api/tasks/{id}` | 任务详情 / 更新（重注册调度）/ 删除 |
| POST | `/api/tasks/{id}/trigger` | 手动触发一次同步 |
| CRUD | `/api/check-tasks` | 检查任务（一致性检查）管理 |
| POST | `/api/check-tasks/{id}/trigger` | 手动发起一次一致性检查 |
| GET | `/api/runs?task_id=&status=&limit=` | 执行历史 |
| GET | `/api/runs/{id}` | 执行详情（running 时附带 rclone 实时进度） |
| GET | `/api/checks?task_id=&status=&limit=` | 一致性检查历史 |
| GET | `/api/checks/{id}` | 检查详情（含差异文件清单） |

交互式文档：服务启动后访问 `http://localhost:8000/docs`（Swagger UI）。

## 管理界面（web/，React）

前端构建产物由 FastAPI 直接托管，**生产部署不再需要 Node 运行时**。

```bash
cd web
npm install --registry=https://registry.npmmirror.com

npm run dev          # 开发模式：http://localhost:5173（vite dev server，代理 /api 等到 :8000）

npm run build        # 生产构建 → web/dist/
poetry run rclone-sync serve    # 一个 Python 进程同时服务 API + 静态站点
# 浏览器访问 http://<host>:8000/ → 自动跳 /login → 登录即用
```

> 单一镜像部署：用多阶段 Dockerfile（node 阶段只 `npm run build`，运行时阶段只 COPY `web/dist`）。

功能：

- **存储管理**：对象存储 CRUD，类型覆盖 rclone 主流后端（s3 含 MinIO/AWS/阿里/腾讯 provider、oss、cos、gcs、azureblob、b2、swift），按类型动态表单 + 高级参数 JSON；创建/更新/删除即时同步 rclone rcd
- **同步任务**：CRUD + 启停开关 + 手动触发；源/目标存储下拉、sync/copy 模式、cron 校验；可为任务绑定"同步前一致性检查"（两端一致跳过同步 / 发现差异继续同步 / 检查出错阻止同步）
- **检查任务**：一致性检查（rclone operations/check）独立管理——CRUD、可选周期调度、手动触发；检查参数可视化配置（oneWay/download/differ/missingOnSrc/missingOnDst/match/combined/error，及 SUM 清单模式 checkFileHash/checkFileFs/checkFileRemote，均含说明与默认值）
- **实时监控**：运行中任务每 5s 刷新，进度条/速度/ETA/当前文件（来自 run.live 字段）
- **历史记录**：按任务/状态/条数过滤查询执行结果与统计；检查记录展示差异/缺失文件清单

## 开发

```bash
poetry run pytest               # 测试（27 个用例）
poetry run ruff check app cli.py tests
poetry run mypy app cli.py
```

## 内网部署 / Nexus 仓库

`scripts/vendor.sh` 把运行期依赖打成 wheel,并通过 `twine` 推送到 Nexus(或
其他 PyPI 代理)。`download` 子命令会先 `rm -rf vendor/` 再用 `poetry export`
导出,自动过滤掉 dev 组(poetry / mypy / ruff / pytest),并强制 `--only-binary=:all:`,
内网目标机器无需 C 编译器。

子命令:`download | upload | all | clean`。

```bash
# 1) 下载到 vendor/wheels/(linux x86_64 / aarch64 + windows 三种 wheel)
./scripts/vendor.sh download

# 2) 上传到 Nexus
export NEXUS_URL=https://nexus.example.com/repository/pypi-internal/
export NEXUS_USERNAME=robot$ci
export NEXUS_PASSWORD=$(cat ~/.nexus-token)
./scripts/vendor.sh upload

# 或一气呵成
./scripts/vendor.sh all
```

环境变量:

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `NEXUS_URL` | upload 时必填 | — | Nexus pypi-hosted 仓库地址 |
| `NEXUS_USERNAME` / `NEXUS_PASSWORD` | upload 时必填 | — | basic auth 凭据 |
| `OUTPUT_DIR` | 否 | `vendor/wheels` | wheel 输出目录 |
| `PLATFORMS` | 否 | `manylinux_2_17_x86_64 manylinux_2_17_aarch64 win_amd64` | 空格分隔的 `--platform` 标签 |
| `PYTHON_VERSION` | 否 | `310` | PEP 425 Python 标签 |
| `SKIP_UPLOAD` | 否 | `0` | `=1` 时 `all` 只下载不传 |

## 主备部署 / 自动切换(HA)

两台 VM 共享一个 MySQL,通过数据库行 + lease 续约实现 leader 选举,无需
外部协调器(etcd/redis/keepalived)。代码路径:`app/cluster/election.py`。

`.env` 增加:

```bash
NODE_ID=                       # 留空则自动用 "<hostname>-<pid>",两台机器必须不同
CLUSTER_NAME=rclone-sync       # 同一集群所有节点必须填同一个名字
HEARTBEAT_INTERVAL_SECONDS=3
LEASE_DURATION_SECONDS=10      # 建议 ≥ 心跳 × 3,容忍一次抖动
```

选举算法:

- 每个节点 3 s 心跳:`UPDATE cluster_nodes SET last_heartbeat = NOW()`
- leader 续 lease:`UPDATE leader_lease SET lease_until = NOW() + 10s WHERE cluster_name = ? AND leader_node_id = ?`
- 30 s 选举周期:CAS `UPDATE leader_lease ... WHERE (leader_node_id IS NULL OR lease_until <= NOW())`,`rowcount == 1` 才算抢到
- 备机接管时间 ≈ lease_duration(默认 10 s)+ elect 周期(30 s),worst case ≈ 40 s

调度与 API 行为:

- **调度闸门**:只有 leader 把 `sync_tasks` / `check_tasks` 注册到 APScheduler;备机的任务 job 在丢权时被 `pause_job`,不会触发新的 rclone
- **API 写闸门**:`POST/PUT/DELETE /api/tasks`、`/api/check-tasks` 在备机上返回 **503** + `Retry-After: 5`,body 含 `leader_id`,便于前端展示或重定向
- **API 读**:两节点都开,前端可任选一台展示集群状态
- **`/healthz`** 新增 `is_leader / node_id / cluster_name / leader_id / peers`,供外部 LB / keepalived 判活
- **`rclone-sync status`** 增加 `cluster` 段,从 DB 直读成员列表,即便 `/healthz` 不可达也能查

部署示例:

```bash
# 节点 A
NODE_ID=nodeA CLUSTER_NAME=rclone-sync \
DATABASE_URL=mysql+pymysql://user:pass@db/rclone_sync \
poetry run rclone-sync serve

# 节点 B(同一 DB)
NODE_ID=nodeB CLUSTER_NAME=rclone-sync \
DATABASE_URL=mysql+pymysql://user:pass@db/rclone_sync \
API_PORT=8001 \
poetry run rclone-sync serve

# 演练故障切换:kill 掉 nodeA,等 ~11 s 后
curl :8001/healthz | jq '.is_leader'   # true
```

## 用户管理与权限

三角色 RBAC(JWT bearer token 鉴权),所有 HA 节点共享同一个 `JWT_SECRET`
即可对同一批用户/令牌进行验证。

### 角色矩阵

| 路由 | admin | edit | view |
|---|---|---|---|
| `POST /api/auth/login`、`GET /api/auth/me`、`POST /api/auth/change-password` | ✓ | ✓ | ✓ |
| `GET /healthz` | public | public | public |
| `GET /api/storages` `/api/storages/{id}` | ✓ | ✓ | ✓ |
| `POST/PUT/DELETE /api/storages` | ✓ | ✓ | ✗ |
| `GET /api/tasks` `/api/tasks/{id}` | ✓ | ✓ | ✓ |
| `POST/PUT/DELETE /api/tasks` | ✓ | ✓ | ✗ |
| `POST /api/tasks/{id}/trigger` | ✓ | ✓ | ✗ |
| `GET /api/runs` `/api/runs/{id}` | ✓ | ✓ | ✓ |
| `GET /api/check-tasks` `/api/check-tasks/{id}` | ✓ | ✓ | ✓ |
| `POST/PUT/DELETE /api/check-tasks` | ✓ | ✓ | ✗ |
| `POST /api/check-tasks/{id}/trigger` | ✓ | ✓ | ✗ |
| `GET /api/checks` `/api/checks/{id}` | ✓ | ✓ | ✓ |
| `/rclone/*` (GET) | ✓ | ✓ | ✓ |
| `/rclone/*` (POST/PUT/DELETE) | ✓ | ✗ | ✗ |
| `GET /api/users` | ✓ | ✗ | ✗ |
| `POST/PUT/DELETE /api/users` | ✓ | ✗ | ✗ |
| `POST /api/users/{id}/reset-password` | ✓ | ✗ | ✗ |

> rclone 写为 admin 独占 — `/config/create` 等会注入上游凭据,泄露代价大。

### 首次部署 / bootstrap admin

`.env` 同时填 `BOOTSTRAP_ADMIN_USER` 和 `BOOTSTRAP_ADMIN_PASSWORD`,启动
时若 `users` 表为空则自动创建该管理员账号(幂等,已有则 no-op)。两个变量
留空 = 不 bootstrap。

```bash
JWT_SECRET=$(openssl rand -hex 32) \
BOOTSTRAP_ADMIN_USER=admin \
BOOTSTRAP_ADMIN_PASSWORD='Sup3r$ecret!' \
poetry run rclone-sync serve
# 启动日志会打 "bootstrap_admin: created admin user 'admin'..."
```

### 登录与改密码

```bash
# 1. 登录拿 token
TOKEN=$(curl -sX POST localhost:8000/api/auth/login \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"Sup3r$ecret!"}' | jq -r .access_token)

# 2. 创建 edit / view 用户(只有 admin 能调)
curl -sX POST localhost:8000/api/users \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"username":"alice","password":"alicepass1","role":"edit"}'

# 3. 改自己密码(任何登录用户)
curl -sX POST localhost:8000/api/auth/change-password \
  -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"old_password":"Sup3r$ecret!","new_password":"new$ecret!"}'
```

### Web UI

打开 `:5173`(dev)或 `:8080`(prod) 自动跳到 `/login`;登录后按 Header 右上
用户名 → "登出" 清 token + 跳回登录页。`admin` 角色左侧 Sider 多一个
"用户管理"入口。

### env 变量(完整)

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `JWT_SECRET` | 生产必填 | `CHANGE-ME-IN-PRODUCTION` | 32+ 随机字节,所有 HA 节点必须一致;留默认时启动 WARNING |
| `JWT_ALGORITHM` | 否 | `HS256` |  |
| `JWT_EXPIRES_MINUTES` | 否 | `480` | access token 有效期,默认 8 h |
| `BCRYPT_ROUNDS` | 否 | `12` | password hash cost factor |
| `BOOTSTRAP_ADMIN_USER` | 首次部署 | `""` | 与 PASSWORD 同时填才生效 |
| `BOOTSTRAP_ADMIN_PASSWORD` | 首次部署 | `""` | 同上 |

### 升级 / 兼容说明

- **breaking change**:此前所有路由无需鉴权,引入本次升级后除 `/healthz`
  与 `/api/auth/login` 外全部要求 `Authorization: Bearer <token>`
- HA 集群内所有节点必须共享同一个 `JWT_SECRET`,否则 token 不能跨节点验证
- 旧部署升级前先把 admin 通过 bootstrap 注入,再让用户走 Web UI 改密
- 紧急情况下忘记 admin 密码:直接停服务,跑 `BOOTSTRAP_*` 重新 bootstrap
  (因为 users 表非空会被忽略);届时手动 `UPDATE users SET password_hash=...`
  重置,或删掉表里那条记录后重启
