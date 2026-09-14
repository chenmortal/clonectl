import logging
import time
from datetime import datetime, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session, sessionmaker

from app.api.storage_sources import _ds_remote_name
from app.models import CheckRun, CheckTask, RunStatus, RunTrigger
from app.rclone.client import RcloneApiError, RcloneClient
from app.services import alerts
from app.services.runner import error_detail
from app.services.storage import ensure_data_source_remote, ensure_remote

logger = logging.getLogger(__name__)

RESULT_KEYS = (
    "success",
    "status",
    "hashType",
    "combined",
    "missingOnSrc",
    "missingOnDst",
    "match",
    "differ",
    "error",
)


def _utcnow() -> datetime:
    return datetime.now(timezone.utc).replace(tzinfo=None)


def _resolve_refs(
    session: Session,
    client: RcloneClient,
    task: CheckTask,
) -> tuple[str, str]:
    """Same contract as ``runner._resolve_refs`` but for check tasks.

    For DataSource refs the final rclone spec is
    ``{remote}:{ds.path}/{task.{side}_path}`` — the data source owns the
    bucket/prefix and the task's path is a subpath *within* it.
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
        raise RuntimeError(f"check task {task.id}: {side} has no storage or data source")

    return _side("src", task.src_path), _side("dst", task.dst_path)


def _join_ds_path(ds_path: str | None, task_subpath: str | None) -> str:
    """See ``runner._join_ds_path`` — same path-joining rules."""
    base = (ds_path or "").rstrip("/")
    if not base or base == "/":
        return (task_subpath or "").lstrip("/") if task_subpath else ""
    if not task_subpath:
        return base
    return f"{base}/{(task_subpath or '').lstrip('/')}"


def run_check(
    session: Session,
    client: RcloneClient,
    check_task_id: int,
    trigger: RunTrigger,
) -> CheckRun:
    task = session.get(CheckTask, check_task_id)
    if task is None:
        raise ValueError(f"check task {check_task_id} not found")

    check = CheckRun(task_id=task.id, status=RunStatus.pending, trigger=trigger)
    session.add(check)
    session.commit()
    session.refresh(check)

    try:
        src_fs, dst_fs = _resolve_refs(session, client, task)
        job_id = client.start_check(src_fs, dst_fs, task.check_options or {})
    except Exception as exc:  # noqa: BLE001
        check.status = RunStatus.failed
        check.started_at = _utcnow()
        check.finished_at = _utcnow()
        check.error = error_detail(exc)
        session.commit()
        session.refresh(check)
        logger.exception("check %s failed to start", check.id)
        alerts.notify_run_failure(check, session)
        session.commit()
        return check

    check.job_id = job_id
    check.status = RunStatus.running
    check.started_at = _utcnow()
    session.commit()
    session.refresh(check)
    return check


def poll_check(session: Session, client: RcloneClient, check_id: int) -> CheckRun | None:
    check = session.get(CheckRun, check_id)
    if check is None or check.status != RunStatus.running or check.job_id is None:
        return check

    try:
        status = client.job_status(check.job_id)
    except RcloneApiError as exc:
        check.status = RunStatus.failed
        check.finished_at = _utcnow()
        check.error = error_detail(exc)
        session.commit()
        session.refresh(check)
        alerts.notify_run_failure(check, session)
        session.commit()
        return check

    if not status.get("finished"):
        return check

    check.finished_at = _utcnow()
    raw_output = status.get("output")
    output: dict = raw_output if isinstance(raw_output, dict) else {}
    check.result = {key: output.get(key) for key in RESULT_KEYS if key in output}

    if not status.get("success"):
        check.status = RunStatus.failed
        check.error = status.get("error") or "rclone check job failed"
    elif output.get("success", False):
        check.status = RunStatus.success
        check.error = None
    else:
        check.status = RunStatus.failed
        differ = len(output.get("differ") or [])
        missing_src = len(output.get("missingOnSrc") or [])
        missing_dst = len(output.get("missingOnDst") or [])
        errors = len(output.get("error") or [])
        check.error = (
            f"一致性检查发现差异：{differ} 个文件不一致，"
            f"源端缺失 {missing_src}，目标端缺失 {missing_dst}，错误 {errors}"
        )
    session.commit()
    session.refresh(check)

    # Alert dispatch: failure fires (dedup via notified_at); success closes
    # out any previously-notified failed run on the same task.
    if check.status == RunStatus.failed:
        alerts.notify_run_failure(check, session)
    else:
        alerts.notify_run_resolved(check, session)
    session.commit()
    return check


def poll_running_checks(session_factory: sessionmaker, client: RcloneClient) -> None:
    with session_factory() as session:
        checks = session.scalars(
            select(CheckRun).where(CheckRun.status == RunStatus.running)
        ).all()
        for check in checks:
            poll_check(session, client, check.id)


def wait_for_check(
    session: Session,
    client: RcloneClient,
    check_id: int,
    timeout_seconds: int,
    interval_seconds: float = 5.0,
) -> CheckRun | None:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        check = poll_check(session, client, check_id)
        if check is None or check.status != RunStatus.running:
            return check
        time.sleep(interval_seconds)
    check = session.get(CheckRun, check_id)
    if check is not None and check.status == RunStatus.running:
        check.status = RunStatus.failed
        check.finished_at = _utcnow()
        check.error = f"check timed out after {timeout_seconds}s"
        session.commit()
        session.refresh(check)
        alerts.notify_run_failure(check, session)
        session.commit()
    return check
