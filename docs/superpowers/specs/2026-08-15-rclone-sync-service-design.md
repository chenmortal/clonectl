# rclone 周期同步服务 设计文档

日期：2026-08-15
状态：已确认

## 1. 目标

构建一个 Python 服务，管理两个对象存储之间的周期性同步：

- 同步任务与存储配置持久化到数据库
- 工作进程从数据库读取任务，通过 HTTP 调用 rclone rcd 的 RC API 下发存储配置与同步任务
- 同步结果回写数据库
- 提供 FastAPI 管理接口与 Typer CLI

## 2. 技术栈

| 项 | 选择 |
|---|---|
| 语言 | Python 3.10（conda 环境 `rclone`） |
| 包管理 | Poetry（`virtualenvs.create=false`，复用 conda 环境） |
| PyPI 镜像 | 阿里云 `https://mirrors.aliyun.com/pypi/simple` |
| Web 框架 | FastAPI + uvicorn |
| CLI | Typer |
| ORM | SQLAlchemy 2.x（同步 API），支持 SQLite 与 MySQL（pymysql 驱动） |
| 调度 | APScheduler 3.x，内嵌于 FastAPI 进程 |
| HTTP 客户端 | httpx（同步） |

## 3. 架构

```
clonectl/
├── pyproject.toml
├── .env.example
├── app/
│   ├── __init__.py
│   ├── main.py          # FastAPI 工厂 + lifespan（启动调度器/托管 rcd）
│   ├── config.py        # pydantic-settings 配置
│   ├── db.py            # engine / session / Base
│   ├── models.py        # ORM 模型
│   ├── schemas.py       # Pydantic schemas
│   ├── scheduler.py     # APScheduler，启动时从 DB 重建 cron 任务
│   ├── rclone/
│   │   ├── client.py    # RC API 客户端
│   │   └── manager.py   # 托管模式：subprocess 管理 rcd 进程
│   ├── services/
│   │   ├── storage.py   # 存储配置服务
│   │   ├── task.py      # 任务服务
│   │   └── runner.py    # 执行：推配置→发任务→轮询→回写结果
│   └── api/
│       ├── storages.py
│       ├── tasks.py
│       └── runs.py
└── cli.py               # Typer 入口
```

## 4. 数据模型

### storage_configs
| 字段 | 类型 | 说明 |
|---|---|---|
| id | int PK | |
| name | str unique | rclone remote 名称 |
| type | str | rclone 后端类型（s3 等） |
| parameters | JSON | endpoint / provider / access_key_id / secret_access_key 等 |
| created_at / updated_at | datetime | |

### sync_tasks
| 字段 | 类型 | 说明 |
|---|---|---|
| id | int PK | |
| name | str | |
| src_storage_id | FK → storage_configs | |
| src_path | str | 源桶/路径 |
| dst_storage_id | FK → storage_configs | |
| dst_path | str | 目标桶/路径 |
| mode | enum(sync, copy) | sync=镜像（删除目标多余文件），copy=仅复制 |
| cron | str | cron 表达式（分 时 日 月 周） |
| enabled | bool | |
| rclone_options | JSON | 附加 rclone 参数（transfers/checkers 等） |
| created_at / updated_at | datetime | |

### sync_runs
| 字段 | 类型 | 说明 |
|---|---|---|
| id | int PK | |
| task_id | FK → sync_tasks | |
| job_id | int nullable | rclone RC jobid |
| status | enum(pending, running, success, failed, skipped) | |
| trigger | enum(schedule, manual) | |
| started_at / finished_at | datetime | |
| error | text nullable | |
| stats | JSON nullable | bytes / transfers / checks / elapsedTime 等 |

## 5. 执行流程

1. APScheduler 按 cron 触发任务
2. Runner 从 DB 读取任务及两端存储配置
3. 若同一任务存在 running 状态的 run → 本次标记 skipped（并发保护）
4. 幂等推送存储配置：`POST /config/create`（name/type/parameters）
5. 发起同步：`POST /sync/sync` 或 `/sync/copy`，body 含 `srcFs`、`dstFs`、`_async: true` 及 rclone_options → 返回 jobid
6. 创建 sync_runs 记录（status=running, job_id）
7. 后台轮询（调度器 interval job，默认 10s）：`POST /job/status` 判断 finished/success/error；`POST /core/stats`（group=`job/{id}`）采集统计
8. 完成后更新 run：status、finished_at、error、stats

## 6. rclone rcd 管理（双模式）

- `RCLONE_MANAGED=false`（默认）：连接外部已运行的 rcd（兼容现有 server.sh），仅做健康检查
- `RCLONE_MANAGED=true`：应用通过 subprocess 拉起 `rclone rcd --rc-addr ... --rc-user ... --rc-pass ...`，退出时终止；提供 CLI `rcd start|stop|status`

RC 调用统一使用 Basic Auth（`RCLONE_RC_USER` / `RCLONE_RC_PASS`）。

## 7. API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /healthz | 健康检查（含 rcd 连通性） |
| CRUD | /api/storages | 存储配置管理 |
| CRUD | /api/tasks | 同步任务管理（写操作自动重注册调度） |
| POST | /api/tasks/{id}/trigger | 手动触发一次 |
| GET | /api/runs?task_id=&status=&limit= | 执行历史 |
| GET | /api/runs/{id} | 执行详情（running 时附带 rclone 实时状态） |

## 8. CLI（Typer）

- `init-db`：创建表结构
- `serve`：启动 FastAPI + APScheduler（uvicorn）
- `rcd start|stop|status`：托管模式下管理 rclone 进程
- `run-once <task-id>`：立即执行指定任务一次

## 9. 配置（.env / pydantic-settings）

```
DATABASE_URL=sqlite:///./clonectl.db
RCLONE_RC_URL=http://localhost:5572
RCLONE_RC_USER=admin
RCLONE_RC_PASS=6051
RCLONE_MANAGED=false
RCLONE_BIN=rclone
POLL_INTERVAL_SECONDS=10
API_HOST=0.0.0.0
API_PORT=8000
```

## 10. 关键决策

- DB 为唯一事实来源；调度器使用内存 JobStore，服务启动时从 DB 中 enabled 任务重建
- 同步 SQLAlchemy + 同步 httpx，FastAPI 路由使用同步函数（线程池执行），避免不必要的异步复杂度
- 存储配置在执行前幂等推送到 rclone，保证 rcd 重启后配置可自动重建
- 密钥明文存于 DB parameters JSON（与 rclone 自身 config 同等安全级别），文档中注明生产环境应限制 DB 访问权限

## 11. 测试与质量

- pytest：SQLite 内存库 + mock rclone client
- 覆盖：存储/任务 CRUD、调度注册与移除、执行流程（成功/失败/并发跳过）、API 端点
- ruff check + mypy

## 12. 验证步骤

1. `python cli.py init-db`
2. `python cli.py serve`
3. API 创建两个 MinIO 存储（复用 config.sh 中的 local-s3 / remote-s3）
4. 创建 sync 任务（cron）+ 手动 trigger
5. 验证 sync_runs 记录与 rclone 实际同步状态一致
