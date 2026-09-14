# rclone 周期同步服务 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建 FastAPI + Typer + Poetry 工程，从数据库读取周期同步任务，经 HTTP 调用 rclone rcd 完成对象存储同步并回写结果。

**Architecture:** 同步 SQLAlchemy（SQLite/MySQL）持久化任务与结果；APScheduler 内嵌 FastAPI 进程按 cron 触发；Runner 幂等推送存储配置后经 RC API 异步发起同步并轮询结果；rclone rcd 支持外部连接与应用托管双模式。

**Tech Stack:** Python 3.10 (conda env `rclone`) · Poetry · FastAPI · Typer · SQLAlchemy 2.x · APScheduler 3.x · httpx · pytest · ruff · mypy

## Global Constraints

- Python 3.10，conda 环境 `rclone`，Poetry `virtualenvs.create=false`
- PyPI 镜像：阿里云 `https://mirrors.aliyun.com/pypi/simple`
- RC API 认证：Basic Auth（`RCLONE_RC_USER`/`RCLONE_RC_PASS`）
- 数据库为唯一事实来源；调度器内存 JobStore，启动时从 DB 重建
- 不添加任何代码注释（用户全局要求）
- 保留现有 4 个 shell 脚本不动

## File Structure

| 文件 | 职责 |
|---|---|
| `pyproject.toml` | Poetry 依赖与工具配置 |
| `.env.example` | 配置样例 |
| `app/config.py` | pydantic-settings Settings |
| `app/db.py` | engine/session_factory/Base/init_db |
| `app/models.py` | StorageConfig / SyncTask / SyncRun ORM |
| `app/schemas.py` | Pydantic 请求/响应模型 |
| `app/rclone/client.py` | RcloneClient：config/create、sync/sync、sync/copy、job/status、core/stats、ping |
| `app/rclone/manager.py` | RcloneManager：subprocess 托管 rcd |
| `app/services/storage.py` | 存储 CRUD + ensure_remote（幂等推送） |
| `app/services/task.py` | 任务 CRUD + 调度联动 |
| `app/services/runner.py` | 执行流程 + 轮询 + 结果回写 |
| `app/scheduler.py` | SchedulerService：注册/移除/重建 cron job |
| `app/api/storages.py` `app/api/tasks.py` `app/api/runs.py` | REST 路由 |
| `app/main.py` | create_app + lifespan |
| `cli.py` | Typer：init-db / serve / rcd / run-once |
| `tests/conftest.py` | SQLite 内存库 fixture + mock RcloneClient + TestClient |
| `tests/test_*.py` | 各模块测试 |

---

### Task 1: 工程骨架与依赖

**Files:** Create `pyproject.toml`, `.env.example`, `.gitignore`, `app/__init__.py` 及包目录

- [ ] 编写 pyproject.toml（fastapi, uvicorn[standard], typer, sqlalchemy>=2, apscheduler>=3.10,<4, httpx, pydantic-settings, pymysql; dev: pytest, ruff, mypy, types-*）
- [ ] `poetry install`
- [ ] 验证 `python -c "import fastapi, sqlalchemy, apscheduler, httpx, typer"`

### Task 2: config + db + models

**Files:** `app/config.py`, `app/db.py`, `app/models.py`

**Interfaces:**
- `get_settings() -> Settings`（字段：database_url, rclone_rc_url, rclone_rc_user, rclone_rc_pass, rclone_managed, rclone_bin, rclone_rc_addr, poll_interval_seconds, api_host, api_port）
- `init_db(engine)` 建表；`get_session()` 依赖注入
- ORM：`StorageConfig(id,name,type,parameters,created_at,updated_at)`、`SyncTask(id,name,src_storage_id,src_path,dst_storage_id,dst_path,mode,cron,enabled,rclone_options,created_at,updated_at)`、`SyncRun(id,task_id,job_id,status,trigger,started_at,finished_at,error,stats)`

- [ ] 测试：内存 SQLite 建表 + 三表插入/关系查询
- [ ] 实现并通过

### Task 3: RcloneClient

**Files:** `app/rclone/client.py`

**Interfaces:**
- `RcloneClient(base_url, username, password, timeout=30)`
- `.ping() -> bool`（GET /core/version）
- `.create_remote(name, type_, parameters)`（POST /config/create）
- `.start_sync(src_fs, dst_fs, options, mode) -> int`（POST /sync/sync|/sync/copy，`_async:true`，返回 jobid）
- `.job_status(job_id) -> dict`（finished/success/error）
- `.job_stats(job_id) -> dict`（core/stats group=job/{id}）
- 抛出 `RcloneApiError`（含 HTTP 状态与响应体）

- [ ] 测试：httpx MockTransport 覆盖成功/失败/异步 jobid 解析
- [ ] 实现并通过

### Task 4: RcloneManager

**Files:** `app/rclone/manager.py`

**Interfaces:**
- `RcloneManager(bin, rc_addr, user, password)`
- `.start()` / `.stop()` / `.is_running() -> bool`（subprocess + PID 检查，日志写 rcd.log）

- [ ] 测试：mock subprocess 验证启动命令参数与 stop 行为
- [ ] 实现并通过

### Task 5: services（storage/task/runner）

**Files:** `app/services/storage.py`, `app/services/task.py`, `app/services/runner.py`

**Interfaces:**
- `ensure_remote(session, client, storage)`：config/create 幂等推送
- `run_task(session_factory, client, task_id, trigger) -> SyncRun`：并发跳过 → 推配置 → 发起 → 写 run
- `poll_run(session_factory, client, run_id)`：查询 job/status+stats，终态回写

- [ ] 测试：成功流程、失败流程、并发跳过、配置推送调用验证
- [ ] 实现并通过

### Task 6: scheduler

**Files:** `app/scheduler.py`

**Interfaces:**
- `SchedulerService(session_factory, client, poll_interval)`
- `.start()`（重建所有 enabled 任务 + 轮询 interval job）/ `.shutdown()`
- `.register_task(task)` / `.remove_task(task_id)`（job id = `task-{id}`，CronTrigger 解析 5 段表达式）

- [ ] 测试：注册/移除/重建（使用 BackgroundScheduler 实例直接验证 get_jobs）
- [ ] 实现并通过

### Task 7: API 路由 + main

**Files:** `app/api/*.py`, `app/main.py`, `app/schemas.py`

- [ ] storages/tasks CRUD、trigger、runs 查询、healthz
- [ ] lifespan：managed 模式拉起 rcd → 启动调度器 → 退出时清理
- [ ] 测试：TestClient 覆盖 CRUD + trigger + runs（mock client）

### Task 8: CLI

**Files:** `cli.py`

- [ ] init-db / serve / rcd start|stop|status / run-once
- [ ] 手动验证 `python cli.py --help`

### Task 9: 质量验证

- [ ] pytest 全绿
- [ ] ruff check 通过
- [ ] mypy 通过（允许对未标注第三方库忽略）
