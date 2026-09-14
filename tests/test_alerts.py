"""Tests for AlertManager v4 webhook dispatch."""
from __future__ import annotations

from datetime import datetime, timezone

import httpx
import pytest

import app.db as db_module
from app.models import (
    CheckRun,
    CheckTask,
    DataSource,
    RunStatus,
    StorageSource,
    SyncMode,
    SyncRun,
    SyncTask,
    SystemSetting,
    User,
    UserRole,
)
from app.services import alerts
from app.services.alerts import build_alertmanager_payload
from app.services.check import run_check, wait_for_check
from app.services.runner import poll_run, run_task


# --- helpers --------------------------------------------------------------


class FakeHttpxClient:
    """Stub the alerts module's httpx.Client; records every POST."""

    def __init__(self):
        self.calls: list[dict] = []
        self.next_status: int = 200
        self.next_error: Exception | None = None

    def post(self, url, json):
        self.calls.append({"url": url, "json": json})
        if self.next_error is not None:
            raise self.next_error
        return httpx.Response(self.next_status, json={})


@pytest.fixture(autouse=True)
def http_fake(monkeypatch):
    """Replace the module-level httpx.Client used by alerts.

    Autouse so every test gets a fresh stub. Tests that want to assert on
    it declare ``http_fake`` as a parameter (autouse fixtures still flow
    through the parameter system).
    """
    fake = FakeHttpxClient()
    monkeypatch.setattr(alerts, "_client", fake)
    yield fake


def _set_alertmanager_url(session, url: str = "http://alert.test/webhook") -> None:
    session.add(SystemSetting(key="alertmanager_url", value=url))
    session.commit()


def _seed_task_and_dss(session, owner) -> SyncTask:
    """Create a sync task with two data sources; return the task."""
    src = StorageSource(name="s3src", type="s3", endpoint="http://s")
    dst = StorageSource(name="s3dst", type="s3", endpoint="http://d")
    session.add_all([src, dst])
    session.flush()
    src_ds = DataSource(
        name="src", storage_source_id=src.id, path="a",
        access_key_id="AK", secret_access_key="SK", owner_user_id=owner.id,
    )
    dst_ds = DataSource(
        name="dst", storage_source_id=dst.id, path="b",
        access_key_id="AK", secret_access_key="SK", owner_user_id=owner.id,
    )
    session.add_all([src_ds, dst_ds])
    session.flush()
    task = SyncTask(
        name="nightly", src_data_source_id=src_ds.id, src_path="/x",
        dst_data_source_id=dst_ds.id, dst_path="/y",
        mode=SyncMode.sync, cron="0 * * * *",
    )
    session.add(task)
    session.commit()
    session.refresh(task)
    return task


class _NoopRclone:
    """All remote pushes succeed; configurable sync/check behavior."""

    def __init__(self, *, sync_job_id=99, fail_start=False, fail_poll=False,
                 success_poll=True):
        self.sync_job_id = sync_job_id
        self.fail_start = fail_start
        self.fail_poll = fail_poll
        self.success_poll = success_poll

    def create_remote(self, name, type_, parameters):
        return {}

    def delete_remote(self, name):
        return {}

    def start_sync(self, src_fs, dst_fs, mode, options=None):
        if self.fail_start:
            from app.rclone.client import RcloneApiError
            raise RcloneApiError("denied", 403, {"error": "creds denied"})
        return self.sync_job_id

    def start_check(self, src_fs, dst_fs, options=None):
        if self.fail_start:
            from app.rclone.client import RcloneApiError
            raise RcloneApiError("denied", 403, {"error": "creds denied"})
        return self.sync_job_id

    def job_status(self, job_id):
        if self.fail_poll:
            return {"finished": True, "success": False, "error": "still denied"}
        if self.success_poll:
            return {"finished": True, "success": True}
        return {"finished": False}

    def job_stats(self, job_id):
        return {"bytes": 1, "transfers": 1, "errors": 0, "elapsedTime": 0.1}


@pytest.fixture()
def seeded(db_engine):
    """Seed an admin owner + a sync task with two data sources."""
    session = db_module.session_factory()
    admin = User(
        username="admin", password_hash="x", role=UserRole.admin,
    )
    session.add(admin)
    session.commit()
    session.refresh(admin)
    _set_alertmanager_url(session)
    task = _seed_task_and_dss(session, admin)
    return {"session": session, "task": task, "admin": admin}


# --- payload shape --------------------------------------------------------


def test_payload_firing_shape():
    p = build_alertmanager_payload(
        task_id=7, task_name="nightly", run_id=42,
        error_text="boom", started_at=datetime(2026, 9, 3, 12, 0, 0),
        status="firing", labels={"alertname": "SyncTaskFailed"}, is_sync=True,
    )
    assert p["version"] == "4"
    assert p["status"] == "firing"
    assert p["groupKey"] == "{run.sync}.7"
    assert p["commonLabels"]["task_id"] == "7"
    assert p["commonLabels"]["alertname"] == "SyncTaskFailed"
    assert p["commonLabels"]["task_name"] == "nightly"
    assert p["commonLabels"]["run_id"] == "42"
    assert p["alerts"][0]["status"] == "firing"
    assert p["alerts"][0]["endsAt"] == "0001-01-01T00:00:00Z"
    assert p["alerts"][0]["startsAt"].endswith("Z")


def test_payload_resolved_shape():
    p = build_alertmanager_payload(
        task_id=7, task_name="nightly", run_id=42,
        error_text="", started_at=None,
        status="resolved", labels={"alertname": "CheckTaskFailed"}, is_sync=False,
        ends_at=datetime(2026, 9, 3, 13, 0, 0),
    )
    assert p["status"] == "resolved"
    assert p["groupKey"] == "{run.check}.7"
    assert p["commonLabels"]["task_kind"] == "check"
    assert not p["alerts"][0]["endsAt"].startswith("0001")


# --- dispatch behavior ----------------------------------------------------


def test_no_url_no_alert(seeded, http_fake):
    """Empty URL = alerts are skipped (no HTTP call)."""
    session = db_module.session_factory()
    row = session.get(SystemSetting, "alertmanager_url")
    row.value = ""
    session.commit()
    run = run_task(seeded["session"], _NoopRclone(fail_start=True),
                   seeded["task"].id, "manual")
    assert http_fake.calls == []


def test_failure_fires_webhook(seeded, http_fake):
    """A failing rclone start → firing webhook + notified_at set."""
    run = run_task(seeded["session"], _NoopRclone(fail_start=True),
                   seeded["task"].id, "manual")
    assert len(http_fake.calls) == 1
    payload = http_fake.calls[0]["json"]
    assert payload["status"] == "firing"
    assert payload["commonLabels"]["alertname"] == "SyncTaskFailed"
    assert payload["commonLabels"]["task_id"] == str(seeded["task"].id)
    session = db_module.session_factory()
    r = session.get(SyncRun, run.id)
    assert r.notified_at is not None


def test_dedup_no_double_alert(seeded, http_fake):
    """Polling the same failed row twice → only one webhook."""
    run = run_task(seeded["session"], _NoopRclone(fail_start=True),
                   seeded["task"].id, "manual")
    assert len(http_fake.calls) == 1
    # Reset the row to running, poll again with a fresh fail — must not re-fire.
    session = db_module.session_factory()
    r = session.get(SyncRun, run.id)
    r.status = RunStatus.running
    session.commit()
    http_fake.calls.clear()
    poll_run(session, _NoopRclone(fail_start=False, fail_poll=True), run.id)
    assert http_fake.calls == []


def test_resolved_fires_after_success(seeded, http_fake):
    """A successful run after a notified failure → resolved webhook."""
    # 1. first run fails and fires
    run1 = run_task(seeded["session"], _NoopRclone(fail_start=True),
                    seeded["task"].id, "manual")
    assert len(http_fake.calls) == 1
    assert http_fake.calls[0]["json"]["status"] == "firing"

    # 2. second run starts + polls → success → resolve fires
    http_fake.calls.clear()
    client = _NoopRclone(success_poll=True)
    run2 = run_task(seeded["session"], client, seeded["task"].id, "manual")
    poll_run(seeded["session"], client, run2.id)
    assert len(http_fake.calls) == 1
    payload = http_fake.calls[0]["json"]
    assert payload["status"] == "resolved"
    assert payload["commonLabels"]["task_id"] == str(seeded["task"].id)

    # notified_at on the original failed run was cleared
    session = db_module.session_factory()
    r1 = session.get(SyncRun, run1.id)
    assert r1.notified_at is None


def test_no_resolve_without_prior_failure(seeded, http_fake):
    """A successful run with no prior failed/notified run → no webhook."""
    client = _NoopRclone(success_poll=True)
    run = run_task(seeded["session"], client, seeded["task"].id, "manual")
    poll_run(seeded["session"], client, run.id)
    # No prior failure → notify_run_resolved short-circuits.
    assert http_fake.calls == []


def test_http_error_does_not_propagate(seeded, http_fake):
    """If AlertManager is down, the poller keeps going."""
    http_fake.next_error = httpx.ConnectError("conn refused")
    # The run start still fails (rclone also fails) → fires anyway, swallows
    run = run_task(seeded["session"], _NoopRclone(fail_start=True),
                   seeded["task"].id, "manual")
    assert run.status == RunStatus.failed
    # notified_at stays None because the POST failed
    session = db_module.session_factory()
    r = session.get(SyncRun, run.id)
    assert r.notified_at is None


def test_skipped_run_does_not_alert(seeded, http_fake):
    """A second concurrent run is marked skipped — no alert for skipped.

    Setup: first run succeeds (status=running, never polled). Trigger
    a second time → the active-run guard inserts a skipped row.
    """
    client = _NoopRclone(success_poll=True)
    run_task(seeded["session"], client, seeded["task"].id, "manual")
    http_fake.calls.clear()
    run2 = run_task(seeded["session"], client, seeded["task"].id, "manual")
    assert run2.status == RunStatus.skipped
    # Skipped → no alert. Success also doesn't alert on its own (no prior
    # notified failure on the task), so total calls = 0.
    assert http_fake.calls == []


# --- check alerts ---------------------------------------------------------


def test_check_failure_alerts(seeded, http_fake):
    """A check run that fails also fires a CheckTaskFailed alert."""
    session = db_module.session_factory()
    task = seeded["task"]
    check_task = CheckTask(
        name="ct", src_data_source_id=task.src_data_source_id, src_path="/x",
        dst_data_source_id=task.dst_data_source_id, dst_path="/y",
    )
    session.add(check_task)
    session.commit()
    session.refresh(check_task)

    http_fake.calls.clear()
    run_check(session, _NoopRclone(fail_start=True), check_task.id, "manual")
    assert len(http_fake.calls) == 1
    assert http_fake.calls[0]["json"]["commonLabels"]["alertname"] == "CheckTaskFailed"


def test_check_timeout_alerts(seeded, http_fake):
    """wait_for_check timeout also fires a failure alert."""
    session = db_module.session_factory()
    task = seeded["task"]
    check_task = CheckTask(
        name="ct", src_data_source_id=task.src_data_source_id, src_path="/x",
        dst_data_source_id=task.dst_data_source_id, dst_path="/y",
    )
    session.add(check_task)
    session.commit()
    session.refresh(check_task)

    http_fake.calls.clear()
    client = _NoopRclone(success_poll=False)  # job_status returns finished=False
    check = run_check(session, client, check_task.id, "manual")
    wait_for_check(session, client, check.id, timeout_seconds=0)
    assert any(
        c["json"]["commonLabels"]["alertname"] == "CheckTaskFailed"
        for c in http_fake.calls
    )
