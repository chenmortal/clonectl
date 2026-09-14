# rclone-sync 对外 API 文档

版本：0.1.0 · Base URL：`http://<host>:8000` · Content-Type：`application/json` · 字符集：UTF-8

服务提供存储配置管理、同步任务管理、执行记录查询三类接口。所有写操作即时生效：任务创建/更新/删除会自动联动调度器（APScheduler）。

交互式文档：Swagger UI `GET /docs` · ReDoc `GET /redoc` · OpenAPI 3.1 规范 `GET /openapi.json`

---

## 1. 通用约定

### 1.1 枚举值

| 枚举 | 取值 | 说明 |
|---|---|---|
| `mode` | `sync` / `copy` | sync=镜像同步（删除目标端多余文件）；copy=仅复制，不删除 |
| `status`（run） | `pending` / `running` / `success` / `failed` / `skipped` | 执行状态，见 §5 状态机 |
| `trigger` | `schedule` / `manual` | 调度触发 / 手动触发（trigger 接口或 run-once 命令） |

### 1.2 错误响应格式

非 2xx 响应统一为：

```json
{ "detail": "错误描述" }
```

| HTTP 状态码 | 场景 |
|---|---|
| 400 | 业务校验失败（如任务引用了不存在的存储） |
| 401 | 未登录 / token 缺失或无效 |
| 403 | 已登录但角色不足 |
| 404 | 资源不存在 |
| 409 | 冲突（存储名重复；删除被任务引用的存储） |
| 422 | 请求体字段校验失败（FastAPI 自动，detail 为数组） |

### 1.3 cron 表达式

5 段格式：`分 时 日 月 周`，如 `0 3 * * *`（每天 03:00）、`*/30 * * * *`（每 30 分钟）。

### 1.4 鉴权

除 `/healthz` 与 `/api/auth/login` 外,所有路由要求 `Authorization: Bearer <token>` 头。

- **三角色**:`admin` / `edit` / `view`,具体矩阵见 README "用户管理与权限" 一节
- **token 有效期**:`JWT_EXPIRES_MINUTES` 默认 480 分钟(8 h),过期返回 401
- **跨节点**:HA 主备共享同一个 `JWT_SECRET` 即可验证同一 token

### 1.5 鉴权响应

| 场景 | HTTP | detail 示例 |
|---|---|---|
| 缺 token | 401 | `{"error": "unauthenticated", "reason": "missing bearer token"}` |
| token 篡改/过期 | 401 | `{"error": "unauthenticated", "reason": "invalid token: ..."}` |
| 角色不足 | 403 | `{"error": "forbidden", "required": ["admin"], "actual": "view"}` |
| 登录失败 | 401 | `{"error": "invalid_credentials"}` |

---

## 1A. 鉴权与用户管理 `/api/auth` `/api/users`

### 1A.1 `POST /api/auth/login` — 登录拿 token

**权限**:public

**请求体**:
```json
{ "username": "admin", "password": "Sup3r$ecret!" }
```

**响应 200**:
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "token_type": "bearer",
  "expires_in": 28800,
  "role": "admin"
}
```

**错误**:401 `{"detail": {"error": "invalid_credentials"}}`(用户名不存在 / 密码错 / 账号停用都返回相同错误,不区分)

### 1A.2 `GET /api/auth/me` — 当前用户信息

**权限**:任何登录用户

**响应 200**:
```json
{ "id": 1, "username": "admin", "role": "admin", "last_login_at": "2026-09-03T07:23:28" }
```

### 1A.3 `POST /api/auth/change-password` — 改自己密码

**权限**:任何登录用户

**请求体**:
```json
{ "old_password": "Sup3r$ecret!", "new_password": "new$ecret!" }
```

`new_password` 至少 8 位。

**响应**:204 No Content

**错误**:401 旧密码错;422 新密码太短。

### 1A.4 `GET /api/users` — 用户列表(admin)

**权限**:admin

**响应 200**:
```json
[
  { "id": 1, "username": "admin", "role": "admin",
    "created_at": "...", "last_login_at": "...", "disabled_at": null }
]
```

### 1A.5 `POST /api/users` — 创建用户(admin)

**权限**:admin

**请求体**:
```json
{ "username": "alice", "password": "alicepass1", "role": "edit" }
```

`role` ∈ `admin` / `edit` / `view`;`password` ≥ 8 位。

**响应 201**:同 1A.4 单条结构

**错误**:409 `{"error": "username_taken"}`;400 `{"error": "invalid_role"}`(role 拼错)

### 1A.6 `PUT /api/users/{id}` — 改角色 / 停用(admin)

**权限**:admin

**请求体**(字段都可省):
```json
{ "role": "view", "disabled": true }
```

**响应 200**:更新后的 UserOut

### 1A.7 `DELETE /api/users/{id}` — 删除用户(admin)

**权限**:admin

**响应**:204

**错误**:400 `{"error": "cannot_delete_self"}`(不能删自己)

### 1A.8 `POST /api/users/{id}/reset-password` — 重置密码(admin)

**权限**:admin

**请求体**:
```json
{ "new_password": "new$ecret!" }
```

**响应**:204

---

## 2. rclone RC 代理 /rclone

主服务将 `/rclone/{path}` 反向代理到 rclone rcd（`RCLONE_RC_URL`，默认 `http://127.0.0.1:5572`），调用方无需直连 5572 端口，也无需提供 rclone 认证（由服务端注入 `RCLONE_RC_USER`/`RCLONE_RC_PASS`）。

- 支持方法：GET / POST / PUT / DELETE / PATCH / OPTIONS / HEAD
- 路径映射：`/rclone/sync/sync` → rcd `/sync/sync`；query 参数与请求体原样转发
- 上游状态码与响应体原样透传；rcd 不可达时返回 502 `{"detail": "rclone rcd unreachable: ..."}`
- 注意：RC 操作接口均为 POST（rclone 约定）；Web GUI 页面经子路径代理时其相对路径资源不可用，GUI 请直连 5572

```bash
curl -X POST http://localhost:8000/rclone/core/version
curl -X POST http://localhost:8000/rclone/sync/sync \
  -H "Content-Type: application/json" \
  -d '{"srcFs": "local-s3:/data", "dstFs": "remote-s3:/data", "_async": true}'
```

---

## 3. 健康检查

### GET /healthz

检查服务与 rclone rcd 的连通性。

**响应 200**

| 字段 | 类型 | 说明 |
|---|---|---|
| status | string | 固定 `ok` |
| rclone_reachable | boolean | rclone rcd RC API 是否可达 |

```bash
curl http://localhost:8000/healthz
# {"status":"ok","rclone_reachable":true}
```

---

## 4. 存储配置 /api/storages

存储配置对应一个 rclone remote。**创建/更新存储时立即通过 RC API `config/create` 幂等推送到 rclone rcd**；推送失败不影响入库（返回 502 提示），任务执行前仍会再次幂等推送兜底，因此 rcd 重启后配置可自动重建。

### Storage 对象

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| id | int | 响应 | 主键 |
| name | string | 是 | rclone remote 名称，全局唯一，1-128 字符 |
| type | string | 是 | rclone 后端类型，如 `s3`、`oss`、`gcs` |
| parameters | object | 是 | 后端参数，原样透传给 rclone（见下例） |
| created_at / updated_at | string(datetime) | 响应 | ISO8601 时间戳 |

S3/MinIO 常用 parameters：`provider`、`access_key_id`、`secret_access_key`、`endpoint`、`region`。

### GET /api/storages

列出全部存储配置。响应 200：`Storage[]`。

### POST /api/storages

创建存储配置。入库成功后立即推送至 rclone rcd。

**请求体**：`name`、`type`、`parameters`（均可缺省为空对象）。

**响应**：201 `Storage` · 409 name 已存在 · 422 校验失败 · 502（已入库但推送 rcd 失败）

```bash
curl -X POST http://localhost:8000/api/storages \
  -H "Content-Type: application/json" \
  -d '{
    "name": "local-s3",
    "type": "s3",
    "parameters": {
      "provider": "Minio",
      "access_key_id": "rustfsadmin",
      "secret_access_key": "rustfsadmin",
      "endpoint": "http://127.0.0.1:9000"
    }
  }'
```

### GET /api/storages/{storage_id}

响应：200 `Storage` · 404

### PUT /api/storages/{storage_id}

部分更新，仅传需要修改的字段。更新成功后立即重新推送至 rclone rcd。响应：200 `Storage` · 404 · 502（推送失败）

```bash
curl -X PUT http://localhost:8000/api/storages/1 \
  -H "Content-Type: application/json" \
  -d '{"parameters": {"provider": "Minio", "endpoint": "http://127.0.0.1:9001"}}'
```

### DELETE /api/storages/{storage_id}

删除存储配置，并同步调用 RC API `config/delete` 从 rclone rcd 移除对应 remote（幂等：rcd 中不存在时不报错）。

响应：204 · 404 · 409（仍被任何任务的 src/dst 引用）· 502（已从 DB 删除但 rcd 移除失败）

---

## 5. 同步任务 /api/tasks

### Task 对象

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| id | int | 响应 | 主键 |
| name | string | 是 | 任务名称，1-128 字符 |
| src_storage_id | int | 是 | 源存储 id |
| src_path | string | 是 | 源路径（桶/前缀），如 `/data` |
| dst_storage_id | int | 是 | 目标存储 id |
| dst_path | string | 是 | 目标路径 |
| mode | enum | 是 | `sync` / `copy` |
| cron | string | 是 | 5 段 cron 表达式 |
| enabled | bool | 否 | 默认 `true`；`false` 时调度器不注册该任务 |
| rclone_options | object | 否 | 附加 rclone 参数，以 `_config` 下发给 RC（等同 curl 调用 sync/sync 的 `_config` 字段）。常用键：`transfers`（并行传输数，默认 4）、`checkers`（并行检查数，默认 8）、`checkFirst`（先检查后传输）、`multiThreadStreams`（多线程下载流，0=关闭）、`s3UploadConcurrency`（S3 上传并发）、`s3ChunkSize`（S3 分片大小，如 "64M"）、`retries`（重试次数，默认 3）、`dryRun`（演练模式）；其余 rclone 参数亦可任意传入 |
| pre_check_task_id | int \| null | 否 | 同步前一致性检查任务 id（见 §6 检查任务）。设置后每次同步前先执行该检查：**两端一致 → 跳过本次同步**；**发现差异 → 继续同步**；**检查本身出错/超时 → 阻止同步**（run 记为 skipped 并注明原因） |
| created_at / updated_at | string(datetime) | 响应 | |

实际下发给 rclone 的远程路径拼接为 `{storage.name}:{path}`，例如 `local-s3:/data`。

### GET /api/tasks

列出全部任务。响应 200：`Task[]`。

### POST /api/tasks

创建任务并自动注册 cron 调度。

**响应**：201 `Task` · 400（src/dst 存储不存在）· 422

```bash
curl -X POST http://localhost:8000/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "name": "nightly-backup",
    "src_storage_id": 1,
    "src_path": "/data",
    "dst_storage_id": 2,
    "dst_path": "/data",
    "mode": "sync",
    "cron": "0 3 * * *",
    "enabled": true,
    "rclone_options": {"transfers": 4}
  }'
```

### GET /api/tasks/{task_id}

响应：200 `Task` · 404

### PUT /api/tasks/{task_id}

部分更新；调度随 enabled/cron 变更自动重注册或移除。

**响应**：200 `Task` · 400 · 404

```bash
curl -X PUT http://localhost:8000/api/tasks/1 \
  -H "Content-Type: application/json" \
  -d '{"cron": "0 4 * * *", "enabled": false}'
```

### DELETE /api/tasks/{task_id}

删除任务及其调度；历史执行记录（sync_runs）级联删除。响应：204 · 404

### POST /api/tasks/{task_id}/trigger

手动触发一次同步（不等待完成，异步执行）。并发保护：若该任务已有 `running` 的执行，则本次记为 `skipped`。

**响应**：202 `Run`（见 §5）· 404

```bash
curl -X POST http://localhost:8000/api/tasks/1/trigger
# {"id":12,"task_id":1,"job_id":468,"status":"running","trigger":"manual",...}
```

---

## 6. 检查任务 /api/check-tasks

一致性检查的独立任务实体（rclone `operations/check`：比对两端文件大小与哈希，不修改源和目标）。可配置周期调度，也可被同步任务通过 `pre_check_task_id` 引用为前置检查。

### CheckTask 对象

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| id | int | 响应 | 主键 |
| name | string | 是 | 检查任务名称 |
| src_storage_id / src_path | int / string | 是 | 源存储与路径 |
| dst_storage_id / dst_path | int / string | 是 | 目标存储与路径 |
| cron | string \| null | 否 | 周期检查的 cron 表达式；`null` 表示仅手动/前置触发 |
| enabled | bool | 否 | 默认 `true` |
| check_options | object | 否 | operations/check 参数：`oneWay`（单向，默认 false）、`download`（下载逐字节比对，默认 false）、`differ`/`missingOnSrc`/`missingOnDst`/`error`（报告项，默认 true）、`match`/`combined`（默认 false）、`checkFileHash`（SUM 清单哈希类型，如 md5）、`checkFileFs` + `checkFileRemote`（SUM 清单文件位置；启用后以清单为源比对目标，srcFs 不再生效） |

### GET /api/check-tasks · POST /api/check-tasks

列表 / 创建（创建时校验存储存在；自动注册周期调度）。响应：200 `CheckTask[]` · 201 `CheckTask` · 400 · 422

### GET / PUT / DELETE /api/check-tasks/{check_task_id}

详情 / 部分更新（调度自动重注册）/ 删除。删除保护：仍被任何同步任务引用为前置检查时返回 409；删除时检查记录（check_runs）级联删除。响应：200 · 204 · 400 · 404 · 409

### POST /api/check-tasks/{check_task_id}/trigger

手动发起一次一致性检查（异步执行）。

**响应**：202 `Check`（见 §7）· 404

```bash
curl -X POST http://localhost:8000/api/check-tasks/1/trigger
# {"id":3,"task_id":1,"job_id":77,"status":"running","trigger":"manual",...}
```

---

## 7. 一致性检查记录 /api/checks

### Check 对象

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | 主键 |
| task_id | int | 所属检查任务（check_tasks） |
| job_id | int \| null | rclone RC 异步 jobid |
| status | enum | pending / running / success（两端一致）/ failed（发现差异、错误或超时） |
| trigger | enum | schedule（周期检查）/ manual（手动或同步前检查继承触发方式） |
| started_at / finished_at | string(datetime) \| null | |
| error | string \| null | 失败原因（含差异数量摘要） |
| result | object \| null | operations/check 输出：`success`、`status`、`hashType`、`differ[]`、`missingOnSrc[]`、`missingOnDst[]`、`match[]`、`error[]`、`combined[]` |

### GET /api/checks

查询检查历史，按 id 倒序。Query 参数：`task_id`、`status`、`limit`（默认 100）。响应 200：`Check[]`

### GET /api/checks/{check_id}

检查详情。`status=running` 时附带 `live` 字段（rclone job/status 实时输出）。响应：200 `CheckDetail` · 404

---

## 8. 执行记录 /api/runs

### Run 对象

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | 主键 |
| task_id | int | 所属任务 |
| job_id | int \| null | rclone RC 异步 jobid |
| status | enum | pending / running / success / failed / skipped |
| trigger | enum | schedule / manual |
| started_at / finished_at | string(datetime) \| null | |
| error | string \| null | 失败原因 |
| stats | object \| null | 完成后采集的统计：`bytes`、`checks`、`transfers`、`errors`、`elapsedTime`、`totalBytes`、`totalTransfers` |

### 状态机

```
触发 ──> pending ──> running ──> success
                 │            └─> failed（rclone 报错 / 启动失败 / 轮询失败）
                 └─> skipped（同一任务已有 running 的执行）
```

### GET /api/runs

查询执行历史，按 id 倒序。

| Query 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| task_id | int | 不过滤 | 按任务过滤 |
| status | enum | 不过滤 | 按状态过滤 |
| limit | int | 100 | 返回条数上限 |

```bash
curl "http://localhost:8000/api/runs?task_id=1&status=failed&limit=20"
```

响应 200：`Run[]`

### GET /api/runs/{run_id}

执行详情。当 `status=running` 时额外返回 `live` 字段：rclone 实时 `job/status` 与 `core/stats`（含当前传输文件、进度百分比、速度），可用于进度展示。

**响应**：200 `RunDetail` · 404

```json
{
  "id": 12,
  "task_id": 1,
  "job_id": 468,
  "status": "running",
  "trigger": "schedule",
  "started_at": "2026-08-15T20:31:40",
  "finished_at": null,
  "error": null,
  "stats": null,
  "live": {
    "status": {"finished": false, "success": false, "error": "", "id": 468},
    "stats": {
      "bytes": 3212509184,
      "totalBytes": 5770968007,
      "speed": 25798704.7,
      "eta": 99,
      "transferring": [{"name": "a.mp4", "percentage": 55}]
    }
  }
}
```

---

## 9. 完整调用示例（端到端）

```bash
# 1. 创建两端存储
SRC=$(curl -s -X POST http://localhost:8000/api/storages -H "Content-Type: application/json" \
  -d '{"name":"local-s3","type":"s3","parameters":{"provider":"Minio","access_key_id":"rustfsadmin","secret_access_key":"rustfsadmin","endpoint":"http://127.0.0.1:9000"}}' | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")
DST=$(curl -s -X POST http://localhost:8000/api/storages -H "Content-Type: application/json" \
  -d '{"name":"remote-s3","type":"s3","parameters":{"provider":"Minio","access_key_id":"rustfsadmin","secret_access_key":"rustfsadmin","endpoint":"http://10.126.126.1:9000"}}' | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")

# 2. 创建周期任务（每天 03:00 镜像同步）
curl -s -X POST http://localhost:8000/api/tasks -H "Content-Type: application/json" \
  -d "{\"name\":\"nightly\",\"src_storage_id\":$SRC,\"src_path\":\"/data\",\"dst_storage_id\":$DST,\"dst_path\":\"/data\",\"mode\":\"sync\",\"cron\":\"0 3 * * *\"}"

# 3. 手动触发并轮询结果
RUN=$(curl -s -X POST http://localhost:8000/api/tasks/1/trigger | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")
curl -s http://localhost:8000/api/runs/$RUN
```
