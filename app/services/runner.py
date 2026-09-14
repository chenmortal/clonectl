import json
import logging
from datetime import datetime, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session, sessionmaker

from app.api.storage_sources import _ds_remote_name
from app.models import DataSource, RunStatus, RunTrigger, SyncRun, SyncTask
from app.rclone.client import RcloneApiError, RcloneClient
from app.services import alerts
from app.services.storage import ensure_data_source_remote, ensure_remote

logger = logging.getLogger(__name__)


def _utcnow() -> datetime:
    return datetime.now(timezone.utc).replace(tzinfo=None)


def error_detail(exc: Exception) -> str:
    if isinstance(exc, RcloneApiError):
        body = exc.body if isinstance(exc.body, dict) else {}
        detail = body.get("error")
        if detail:
            return f"{exc}\nrclone: {detail}"
        if body:
            return f"{exc}\n{json.dumps(body, ensure_ascii=False)}"
    return str(exc)


def _resolve_refs(
    session: Session,
    client: RcloneClient,
    task: SyncTask,
) -> tuple[str, str]:
    """Push the appropriate remotes and return ``(src_fs, dst_fs)``.

    Honors both legacy (``src_storage_id``) and v2 (``src_data_source_id``)
    references. The Pydantic schema + API layer guarantee exactly one is set
    per side; this function picks based on which is non-null.

    For DataSource refs the final rclone spec is
    ``{remote}:{ds.path}/{task.{side}_path}`` — the data source owns the
    bucket/prefix and the task's path is a subpath *within* it. For
    legacy StorageConfig refs (no per-source path) the task path is used
    as-is.
    """
    def _side(side: str, path: str) -> str:
        ds = getattr(task, f"{side}_data_source")
        cfg = getattr(task, f"{side}_storage")
        if ds is not None:
            ensure_data_source_remote(session, client, ds)
            return f"{_ds_remote_name(ds)}:{_join_ds_path(ds.path, path)}"
        if cfg is not None:
            ensure_remote(session, client, cfg)
            return f"{cfg.name}:{path}"
        # Should be unreachable: the Pydantic validator rejects both-null
        # and the API layer checks both FKs exist.
        raise RuntimeError(f"task {task.id}: {side} has no storage or data source")

    return _side("src", task.src_path), _side("dst", task.dst_path)


def _join_ds_path(ds_path: str | None, task_subpath: str | None) -> str:
    """Compose ``{ds_path}/{task_subpath}`` with sane slashes.

    Stripping rules:
    - Trailing slashes on ``ds_path`` are removed (avoid double ``//``).
    - Leading slashes on ``task_subpath`` are removed.
    - An empty / ``"/"``-only ``ds_path`` is treated as "no prefix"; the
      returned path is just the (slash-stripped) task subpath.
    - If both are empty the result is ``""``.
    """
    base = (ds_path or "").rstrip("/")
    sub = (task_subpath or "").lstrip("/")
    if not base or base == "/":
        return sub
    if not sub:
        return base
    return f"{base}/{sub}"


def run_task(
    session: Session,
    client: RcloneClient,
    task_id: int,
    trigger: RunTrigger,
) -> SyncRun:
    from app.config import get_settings
    from app.models import CheckRun
    from app.services.check import run_check, wait_for_check

    task = session.get(SyncTask, task_id)
    if task is None:
        raise ValueError(f"task {task_id} not found")

    active = session.scalar(
        select(SyncRun).where(
            SyncRun.task_id == task_id,
            SyncRun.status.in_([RunStatus.pending, RunStatus.running]),
        )
    )
    if active is not None:
        run = SyncRun(
            task_id=task_id,
            status=RunStatus.skipped,
            trigger=trigger,
            started_at=_utcnow(),
            finished_at=_utcnow(),
            error="previous run still running",
        )
        session.add(run)
        session.commit()
        session.refresh(run)
        return run

    run = SyncRun(task_id=task_id, status=RunStatus.pending, trigger=trigger)
    session.add(run)
    session.commit()
    session.refresh(run)

    try:
        src_fs, dst_fs = _resolve_refs(session, client, task)

        if task.pre_check_task_id is not None:
            started_check = run_check(session, client, task.pre_check_task_id, trigger)
            pre_check: CheckRun | None = wait_for_check(
                session, client, started_check.id, get_settings().check_timeout_seconds
            )
            if pre_check is not None and pre_check.status == RunStatus.success:
                run.status = RunStatus.skipped
                run.started_at = _utcnow()
                run.finished_at = _utcnow()
                run.error = (
                    f"同步前一致性检查通过（check #{pre_check.id}）：两端一致，跳过同步"
                )
                session.commit()
                session.refresh(run)
                logger.info("run %s skipped: source and destination match", run.id)
                return run
            differ_found = (
                pre_check is not None
                and pre_check.result is not None
                and pre_check.result.get("success") is False
            )
            if differ_found:
                logger.info(
                    "run %s proceeding: pre-sync check found differences (check %s)",
                    run.id,
                    pre_check.id if pre_check else "?",
                )
            else:
                run.status = RunStatus.skipped
                run.started_at = _utcnow()
                run.finished_at = _utcnow()
                run.error = (
                    f"同步前一致性检查出错（check #{pre_check.id if pre_check else '?'}）："
                    f"{pre_check.error if pre_check else 'unknown'}，已阻止同步"
                )
                session.commit()
                session.refresh(run)
                logger.warning("run %s blocked by pre-sync check error", run.id)
                return run

        job_id = client.start_sync(src_fs, dst_fs, task.mode.value, task.rclone_options)
    except Exception as exc:  # noqa: BLE001
        run.status = RunStatus.failed
        run.started_at = _utcnow()
        run.finished_at = _utcnow()
        run.error = error_detail(exc)
        session.commit()
        session.refresh(run)
        logger.exception("run %s failed to start", run.id)
        alerts.notify_run_failure(run, session)
        session.commit()
        return run

    run.job_id = job_id
    run.status = RunStatus.running
    run.started_at = _utcnow()
    session.commit()
    session.refresh(run)
    return run


def poll_run(session: Session, client: RcloneClient, run_id: int) -> SyncRun | None:
    run = session.get(SyncRun, run_id)
    if run is None or run.status != RunStatus.running or run.job_id is None:
        return run

    try:
        status = client.job_status(run.job_id)
    except RcloneApiError as exc:
        run.status = RunStatus.failed
        run.finished_at = _utcnow()
        run.error = error_detail(exc)
        session.commit()
        session.refresh(run)
        alerts.notify_run_failure(run, session)
        session.commit()
        return run

    if not status.get("finished"):
        return run

    stats: dict = {}
    try:
        stats = client.job_stats(run.job_id)
    except RcloneApiError:
        pass

    run.finished_at = _utcnow()
    new_status_failed = not status.get("success")
    if new_status_failed:
        run.status = RunStatus.failed
        run.error = status.get("error") or "unknown rclone error"
    else:
        run.status = RunStatus.success
    run.stats = {
        key: stats.get(key)
        for key in (
            "bytes",
            "checks",
            "transfers",
            "errors",
            "elapsedTime",
            "totalBytes",
            "totalTransfers",
        )
        if stats.get(key) is not None
    }
    if new_status_failed:
        run.stats["jobStatus"] = status
        if stats:
            run.stats["jobStats"] = stats
    session.commit()
    session.refresh(run)

    # Alert dispatch: dedup is owned by ``alerts.notify_run_failure`` via
    # ``notified_at``. A fresh failure on this run fires; ``notify_run_resolved``
    # is only sent when this run's success implies a previously-notified
    # failure on the same task should be cleared.
    if new_status_failed:
        alerts.notify_run_failure(run, session)
    else:
        alerts.notify_run_resolved(run, session)
    session.commit()
    return run


def poll_running_runs(session_factory: sessionmaker, client: RcloneClient) -> None:
    with session_factory() as session:
        runs = session.scalars(
            select(SyncRun).where(SyncRun.status == RunStatus.running)
        ).all()
        for run in runs:
            poll_run(session, client, run.id)
