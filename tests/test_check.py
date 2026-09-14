import app.db as db_module
from app.models import (
    CheckRun,
    CheckTask,
    RunStatus,
    RunTrigger,
    StorageConfig,
    SyncMode,
    SyncTask,
)
from app.services.check import poll_check, poll_running_checks, run_check
from app.services.runner import run_task

CHECK_OK = {
    "finished": True,
    "success": True,
    "output": {"success": True, "status": "OK", "differ": [], "missingOnSrc": []},
}
CHECK_DIFFER = {
    "finished": True,
    "success": True,
    "output": {
        "success": False,
        "status": "2 differences found",
        "differ": ["a.txt", "b.txt"],
        "missingOnSrc": [],
        "missingOnDst": ["c.txt"],
        "error": [],
    },
}
CHECK_RC_ERROR = {"finished": True, "success": False, "error": "connection refused"}


def _make_storages(session, suffix=""):
    src = StorageConfig(
        name=f"local-s3{suffix}", type="s3", parameters={"endpoint": "http://a"}
    )
    dst = StorageConfig(
        name=f"remote-s3{suffix}", type="s3", parameters={"endpoint": "http://b"}
    )
    session.add_all([src, dst])
    session.commit()
    return src, dst


def _make_check_task(session, cron=None):
    src, dst = _make_storages(session, "-c")
    check_task = CheckTask(
        name="check",
        src_storage_id=src.id,
        src_path="/data",
        dst_storage_id=dst.id,
        dst_path="/backup",
        cron=cron,
        enabled=True,
        check_options={"oneWay": True},
    )
    session.add(check_task)
    session.commit()
    session.refresh(check_task)
    return check_task


def _make_sync_task(session, pre_check_task_id=None):
    src, dst = _make_storages(session, "-s")
    task = SyncTask(
        name="backup",
        src_storage_id=src.id,
        src_path="/data",
        dst_storage_id=dst.id,
        dst_path="/backup",
        mode=SyncMode.sync,
        cron="0 3 * * *",
        enabled=True,
        rclone_options={},
        pre_check_task_id=pre_check_task_id,
    )
    session.add(task)
    session.commit()
    session.refresh(task)
    return task


def test_run_check_success(session, fake_client):
    check_task = _make_check_task(session)
    check = run_check(session, fake_client, check_task.id, RunTrigger.manual)

    assert check.status == RunStatus.running
    assert check.job_id == 77
    assert fake_client.checks[0]["srcFs"] == "local-s3-c:/data"
    assert fake_client.checks[0]["options"] == {"oneWay": True}

    fake_client.job_statuses[77] = CHECK_OK
    poll_check(session, fake_client, check.id)

    refreshed = session.get(CheckRun, check.id)
    assert refreshed.status == RunStatus.success
    assert refreshed.result["status"] == "OK"


def test_run_check_differ_marks_failed(session, fake_client):
    check_task = _make_check_task(session)
    check = run_check(session, fake_client, check_task.id, RunTrigger.schedule)
    fake_client.job_statuses[77] = CHECK_DIFFER
    poll_check(session, fake_client, check.id)

    refreshed = session.get(CheckRun, check.id)
    assert refreshed.status == RunStatus.failed
    assert "2 个文件不一致" in refreshed.error
    assert "目标端缺失 1" in refreshed.error
    assert refreshed.result["differ"] == ["a.txt", "b.txt"]


def test_poll_running_checks(session, fake_client):
    check_task = _make_check_task(session)
    check = run_check(session, fake_client, check_task.id, RunTrigger.manual)
    fake_client.job_statuses[77] = CHECK_OK

    poll_running_checks(db_module.session_factory, fake_client)

    session.expire_all()
    refreshed = session.get(CheckRun, check.id)
    assert refreshed.status == RunStatus.success


def test_pre_check_consistent_skips_sync(session, fake_client):
    check_task = _make_check_task(session)
    task = _make_sync_task(session, pre_check_task_id=check_task.id)
    fake_client.job_statuses[77] = CHECK_OK

    run = run_task(session, fake_client, task.id, RunTrigger.manual)

    assert run.status == RunStatus.skipped
    assert "两端一致，跳过同步" in run.error
    assert len(fake_client.started) == 0


def test_pre_check_differ_proceeds_sync(session, fake_client):
    check_task = _make_check_task(session)
    task = _make_sync_task(session, pre_check_task_id=check_task.id)
    fake_client.job_statuses[77] = CHECK_DIFFER

    run = run_task(session, fake_client, task.id, RunTrigger.manual)

    assert run.status == RunStatus.running
    assert len(fake_client.started) == 1


def test_pre_check_error_blocks_sync(session, fake_client):
    check_task = _make_check_task(session)
    task = _make_sync_task(session, pre_check_task_id=check_task.id)
    fake_client.job_statuses[77] = CHECK_RC_ERROR

    run = run_task(session, fake_client, task.id, RunTrigger.manual)

    assert run.status == RunStatus.skipped
    assert "已阻止同步" in run.error
    assert len(fake_client.started) == 0
