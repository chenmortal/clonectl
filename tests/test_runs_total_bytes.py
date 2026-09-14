"""Tests for /api/runs/{id} live-data merge (totalBytes from /job/status)."""
import pytest

from app.models import (
    DataSource,
    RunStatus,
    RunTrigger,
    StorageSource,
    SyncMode,
    SyncRun,
    SyncTask,
    User,
    UserRole,
)
from app.auth.security import hash_password


def _seed_running_run(db_engine):
    """Create an admin + sync task + a SyncRun in 'running' state."""
    import app.db as db_module
    session = db_module.session_factory()
    admin = User(
        username="admin", password_hash=hash_password("p" * 12),
        role=UserRole.admin,
    )
    session.add(admin)
    session.commit()
    session.refresh(admin)
    src = StorageSource(name="s3", type="s3", endpoint="http://s")
    dst = StorageSource(name="dst", type="s3", endpoint="http://d")
    session.add_all([src, dst])
    session.flush()
    sds = DataSource(
        name="src", storage_source_id=src.id, path="b",
        access_key_id="AK", secret_access_key="SK",
        owner_user_id=admin.id,
    )
    dds = DataSource(
        name="dst", storage_source_id=dst.id, path="b",
        access_key_id="AK", secret_access_key="SK",
        owner_user_id=admin.id,
    )
    session.add_all([sds, dds])
    session.flush()
    task = SyncTask(
        name="t", src_data_source_id=sds.id, src_path="/x",
        dst_data_source_id=dds.id, dst_path="/y",
        mode=SyncMode.sync, cron="0 * * * *",
    )
    session.add(task)
    session.commit()
    session.refresh(task)

    run = SyncRun(
        task_id=task.id, job_id=99, status=RunStatus.running,
        trigger=RunTrigger.manual,
    )
    session.add(run)
    session.commit()
    session.refresh(run)
    return run.id


@pytest.fixture()
def running_run_id(db_engine):
    return _seed_running_run(db_engine)


def test_run_detail_merges_totalbytes(authed_app_client, fake_client, running_run_id):
    """/core/stats lacks totalBytes but /job/status has it — the merge must
    lift it into live.stats so the frontend's bytes / totalBytes formula
    can compute a real percentage (rather than 0%)."""
    # FakeRcloneClient default job_stats has no totalBytes; default job_status
    # also doesn't. Wire up custom responses.
    fake_client.job_stats_map[99] = {
        "bytes": 100, "transfers": 1, "errors": 0, "elapsedTime": 1.5,
        "speed": 0, "transferring": [],
    }
    fake_client.job_statuses[99] = {
        "finished": False,
        "success": False,
        "stats": {
            "bytes": 100,
            "totalBytes": 1000,        # only here, on /job/status
            "transfers": 1,
        },
    }

    r = authed_app_client.get(f"/api/runs/{running_run_id}")
    assert r.status_code == 200
    live = r.json().get("live") or {}
    stats = live.get("stats") or {}
    assert stats.get("bytes") == 100
    assert stats.get("totalBytes") == 1000, (
        "totalBytes from /job/status must be merged into live.stats "
        "so the frontend can compute progress percentage"
    )


def test_run_detail_without_totalbytes(authed_app_client, fake_client, running_run_id):
    """If neither endpoint returns totalBytes (rare, early in job), live.stats
    just doesn't have the field; no error is raised."""
    fake_client.job_stats_map[99] = {
        "bytes": 50, "transfers": 0, "errors": 0, "elapsedTime": 0.5,
        "speed": 100, "transferring": [],
    }
    fake_client.job_statuses[99] = {
        "finished": False, "success": False,
        "stats": {"bytes": 50, "transfers": 0},
        # no totalBytes here either
    }

    r = authed_app_client.get(f"/api/runs/{running_run_id}")
    assert r.status_code == 200
    stats = (r.json().get("live") or {}).get("stats") or {}
    assert stats.get("bytes") == 50
    assert "totalBytes" not in stats


def test_run_detail_preserves_existing_totalbytes(authed_app_client, fake_client, running_run_id):
    """If /core/stats already has totalBytes (some rclone versions), the merge
    does not overwrite it."""
    fake_client.job_stats_map[99] = {
        "bytes": 100, "totalBytes": 500, "transfers": 1, "errors": 0,
        "speed": 0, "transferring": [],
    }
    fake_client.job_statuses[99] = {
        "finished": False, "success": False,
        "stats": {"bytes": 100, "totalBytes": 999},  # different!
    }

    r = authed_app_client.get(f"/api/runs/{running_run_id}")
    stats = (r.json().get("live") or {}).get("stats") or {}
    # /core/stats wins (we only fill in when missing)
    assert stats.get("totalBytes") == 500


def test_run_detail_rclone_error(authed_app_client, fake_client, running_run_id):
    """If job_status fails, live becomes {error: ...} — no crash."""
    from app.rclone.client import RcloneApiError
    def boom(job_id):
        raise RcloneApiError("rcd down", 502, {"error": "down"})
    fake_client.job_status = boom

    r = authed_app_client.get(f"/api/runs/{running_run_id}")
    assert r.status_code == 200
    live = r.json().get("live") or {}
    assert "error" in live
