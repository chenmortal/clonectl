"""AlertManager v4 webhook integration.

Public surface:
- ``notify_run_failure(run, session)`` — called from ``runner.py`` and
  ``check.py`` when a run/check transitions to ``failed``.
- ``notify_run_resolved(run, session)`` — called when a previously failed
  run/check transitions to ``success``.

Both functions:
1. Read ``SystemSetting("alertmanager_url")``; if empty, return.
2. Build a v4 webhook payload (see ``build_alertmanager_payload``).
3. POST via a module-level ``httpx.Client`` (sync, 5 s timeout).
4. Set ``run.notified_at`` on fire; clear it on resolve (for dedup).

Failure mode: HTTP errors are logged at WARNING and swallowed. The
poller must not crash because of an alert delivery failure.

Dedup: ``run.notified_at`` non-null → skip. The poller re-reads failed
rows every interval; this prevents duplicate alerts on re-entry.
``skipped`` status never alerts.
"""
from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import Any

import httpx
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models import (
    CheckRun,
    RunStatus,
    SyncRun,
    SystemSetting,
)

logger = logging.getLogger(__name__)


_client: httpx.Client | None = None


def get_client() -> httpx.Client:
    global _client
    if _client is None:
        _client = httpx.Client(timeout=5.0)
    return _client


def close() -> None:
    global _client
    if _client is not None:
        _client.close()
        _client = None


def _to_iso_z(dt: datetime | None) -> str:
    """Format ``dt`` as RFC3339 with Z suffix. Returns ``'0001-01-01T00:00:00Z'``
    for None — AlertManager's "still firing" sentinel."""
    if dt is None:
        return "0001-01-01T00:00:00Z"
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    else:
        dt = dt.astimezone(timezone.utc)
    # Python's isoformat uses +00:00; AlertManager wants Z.
    return dt.strftime("%Y-%m-%dT%H:%M:%SZ")


def get_alertmanager_url(session: Session) -> str:
    row = session.get(SystemSetting, "alertmanager_url")
    return (row.value if row else "") or ""


def build_alertmanager_payload(
    *,
    task_id: int,
    task_name: str,
    run_id: int,
    error_text: str,
    started_at: datetime | None,
    status: str,
    labels: dict[str, str],
    is_sync: bool,
    ends_at: datetime | None = None,
) -> dict[str, Any]:
    """Build an AlertManager v4 webhook payload.

    ``status`` is ``"firing"`` for a fresh failure and ``"resolved"`` when a
    previously-failed run becomes healthy again. ``ends_at`` is the
    resolution time (now) when ``status="resolved"``; ignored otherwise.
    """
    common_labels = {
        "alertname": labels.get("alertname", "SyncTaskFailed"),
        "severity": "critical",
        "service": "rclone-sync",
        "task_id": str(task_id),
        "task_name": task_name,
        "task_kind": "sync" if is_sync else "check",
        "run_id": str(run_id),
        **labels,
    }
    common_annotations = {
        "summary": f"{'rclone sync' if is_sync else 'rclone check'} failed: "
                   f"{task_name} (run #{run_id})",
        "description": error_text or "unknown rclone error",
        "run_url": f"/runs/{run_id}" if is_sync else f"/checks/{run_id}",
    }
    if status == "resolved":
        ends_at_iso = _to_iso_z(ends_at or datetime.now(timezone.utc))
    else:
        ends_at_iso = "0001-01-01T00:00:00Z"
    kind = "sync" if is_sync else "check"
    return {
        "version": "4",
        "groupKey": f"{{run.{kind}}}.{task_id}",
        "status": status,
        "receiver": "rclone-sync",
        "groupLabels": {
            "alertname": common_labels["alertname"],
            "task_id": str(task_id),
        },
        "commonLabels": common_labels,
        "commonAnnotations": common_annotations,
        "externalURL": "",
        "alerts": [
            {
                "status": status,
                "labels": dict(common_labels),
                "annotations": dict(common_annotations),
                "startsAt": _to_iso_z(started_at),
                "endsAt": ends_at_iso,
                "generatorURL": "",
            }
        ],
    }


def post_to_alertmanager(
    url: str, payload: dict[str, Any]
) -> tuple[bool, int, str | None]:
    """POST a v4 payload to AlertManager. Returns ``(sent, status_code, error)``.

    Errors are NOT raised — the caller logs them. The poller must keep going.
    """
    if not url:
        return False, 0, "no URL"
    try:
        resp = get_client().post(url, json=payload)
    except httpx.HTTPError as exc:
        logger.warning(
            "alertmanager POST %s failed: %s", url, exc,
        )
        return False, 0, str(exc)
    if resp.status_code >= 400:
        logger.warning(
            "alertmanager %s returned %d: %s",
            url, resp.status_code, resp.text[:500],
        )
        return False, resp.status_code, resp.text[:500]
    return True, resp.status_code, None


def _should_fire(run: SyncRun | CheckRun) -> bool:
    """``notified_at`` dedup: don't re-fire if already fired for this row."""
    return run.notified_at is None


def _set_notified(run: SyncRun | CheckRun) -> None:
    """Mark a run as notified (fired). Caller persists."""
    run.notified_at = datetime.now(timezone.utc).replace(tzinfo=None)


def _clear_notified(run: SyncRun | CheckRun) -> None:
    """Clear notified_at on resolve so future failures can re-fire."""
    run.notified_at = None


def notify_run_failure(run: SyncRun | CheckRun, session: Session) -> None:
    """Send a ``firing`` webhook for a failed run/check.

    Skipped runs/checks do NOT alert — caller must check status before
    calling. ``notified_at`` non-null → skipped (dedup).
    """
    if run.status != RunStatus.failed:
        return
    if not _should_fire(run):
        return
    url = get_alertmanager_url(session)
    if not url:
        logger.debug(
            "alertmanager_url not configured; skipping alert for run %s", run.id,
        )
        return
    is_sync = isinstance(run, SyncRun)
    task = run.task
    payload = build_alertmanager_payload(
        task_id=task.id,
        task_name=task.name,
        run_id=run.id,
        error_text=run.error or "unknown rclone error",
        started_at=run.started_at,
        status="firing",
        labels={
            "alertname": "SyncTaskFailed" if is_sync else "CheckTaskFailed",
        },
        is_sync=is_sync,
    )
    sent, _code, _err = post_to_alertmanager(url, payload)
    if sent:
        _set_notified(run)
        logger.info(
            "alertmanager notified: run %s status=firing task=%s",
            run.id, task.id,
        )


def notify_run_resolved(run: SyncRun | CheckRun, session: Session) -> None:
    """Send a ``resolved`` webhook when a fresh success closes a prior firing.

    The poller only re-reads ``running`` rows, so a successful run we
    observe here was never marked failed. We use it as the trigger to
    close out any previously-notified failure for the same task
    (``groupKey == task_id``). After firing, clear ``notified_at`` on the
    previously-notified row so the *next* failure can fire fresh.
    """
    if run.status != RunStatus.success:
        return
    url = get_alertmanager_url(session)
    if not url:
        return
    is_sync = isinstance(run, SyncRun)
    task = run.task

    # Find any failed run on the same task that was notified. Without one,
    # there's nothing to resolve.
    RunModel = SyncRun if is_sync else CheckRun
    prev_notified = session.scalar(
        select(RunModel).where(
            RunModel.task_id == task.id,
            RunModel.status == RunStatus.failed,
            RunModel.notified_at.is_not(None),
        ).order_by(RunModel.id.desc()).limit(1)
    )
    if prev_notified is None:
        return

    payload = build_alertmanager_payload(
        task_id=task.id,
        task_name=task.name,
        run_id=run.id,
        error_text="",
        started_at=prev_notified.started_at,
        status="resolved",
        labels={
            "alertname": "SyncTaskFailed" if is_sync else "CheckTaskFailed",
        },
        is_sync=is_sync,
        ends_at=datetime.now(timezone.utc),
    )
    sent, _code, _err = post_to_alertmanager(url, payload)
    _clear_notified(prev_notified)
    if sent:
        logger.info(
            "alertmanager notified: run %s status=resolved task=%s (closes run %s)",
            run.id, task.id, prev_notified.id,
        )
