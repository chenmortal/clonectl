import app.db as db_module
from app.models import RunStatus, RunTrigger, StorageConfig, SyncMode, SyncRun, SyncTask
from app.services.runner import poll_run, poll_running_runs, run_task


def _make_task(session):
    src = StorageConfig(name="local-s3", type="s3", parameters={"endpoint": "http://a"})
    dst = StorageConfig(name="remote-s3", type="s3", parameters={"endpoint": "http://b"})
    session.add_all([src, dst])
    session.commit()
    task = SyncTask(
        name="backup",
        src_storage_id=src.id,
        src_path="/data",
        dst_storage_id=dst.id,
        dst_path="/backup",
        mode=SyncMode.sync,
        cron="0 3 * * *",
        enabled=True,
        rclone_options={"transfers": 2},
    )
    session.add(task)
    session.commit()
    session.refresh(task)
    return task


def test_run_task_success_flow(session, fake_client):
    task = _make_task(session)
    run = run_task(session, fake_client, task.id, RunTrigger.manual)

    assert run.status == RunStatus.running
    assert run.job_id == 42
    assert [r["name"] for r in fake_client.remotes] == ["local-s3", "remote-s3"]
    assert fake_client.started[0]["srcFs"] == "local-s3:/data"
    assert fake_client.started[0]["dstFs"] == "remote-s3:/backup"
    assert fake_client.started[0]["mode"] == "sync"

    fake_client.job_statuses[42] = {"finished": True, "success": True, "error": ""}
    fake_client.job_stats_map[42] = {"bytes": 100, "transfers": 2, "elapsedTime": 1.0}
    poll_run(session, fake_client, run.id)

    refreshed = session.get(SyncRun, run.id)
    assert refreshed.status == RunStatus.success
    assert refreshed.finished_at is not None
    assert refreshed.stats["bytes"] == 100


def test_run_task_failure_result(session, fake_client):
    task = _make_task(session)
    run = run_task(session, fake_client, task.id, RunTrigger.schedule)
    fake_client.job_statuses[42] = {"finished": True, "success": False, "error": "access denied"}
    poll_run(session, fake_client, run.id)

    refreshed = session.get(SyncRun, run.id)
    assert refreshed.status == RunStatus.failed
    assert refreshed.error == "access denied"
    assert refreshed.stats["jobStatus"]["error"] == "access denied"


def test_run_task_skipped_when_running(session, fake_client):
    task = _make_task(session)
    first = run_task(session, fake_client, task.id, RunTrigger.manual)
    assert first.status == RunStatus.running

    second = run_task(session, fake_client, task.id, RunTrigger.schedule)
    assert second.status == RunStatus.skipped
    assert len(fake_client.started) == 1


def test_run_task_start_failure(session, fake_client):
    from app.rclone.client import RcloneApiError

    task = _make_task(session)
    fake_client.start_exception = RcloneApiError("boom")
    run = run_task(session, fake_client, task.id, RunTrigger.manual)
    assert run.status == RunStatus.failed
    assert "boom" in run.error


def test_run_task_start_failure_includes_rclone_detail(session, fake_client):
    from app.rclone.client import RcloneApiError

    task = _make_task(session)
    fake_client.start_exception = RcloneApiError(
        "rclone rc /sync/sync returned 500", 500, {"error": "directory not found"}
    )
    run = run_task(session, fake_client, task.id, RunTrigger.manual)
    assert run.status == RunStatus.failed
    assert "directory not found" in run.error


def test_poll_running_runs(session, fake_client):
    task = _make_task(session)
    run = run_task(session, fake_client, task.id, RunTrigger.manual)
    fake_client.job_statuses[42] = {"finished": True, "success": True, "error": ""}

    poll_running_runs(db_module.session_factory, fake_client)

    session.expire_all()
    refreshed = session.get(SyncRun, run.id)
    assert refreshed.status == RunStatus.success
