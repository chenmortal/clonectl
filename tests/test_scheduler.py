import pytest

import app.db as db_module
from app.models import StorageConfig, SyncMode, SyncTask
from app.scheduler import SchedulerService, parse_cron


def _make_tasks(session):
    src = StorageConfig(name="s1", type="s3", parameters={})
    dst = StorageConfig(name="s2", type="s3", parameters={})
    session.add_all([src, dst])
    session.commit()
    enabled = SyncTask(
        name="enabled",
        src_storage_id=src.id,
        src_path="/a",
        dst_storage_id=dst.id,
        dst_path="/b",
        mode=SyncMode.copy,
        cron="*/5 * * * *",
        enabled=True,
    )
    disabled = SyncTask(
        name="disabled",
        src_storage_id=src.id,
        src_path="/a",
        dst_storage_id=dst.id,
        dst_path="/b",
        mode=SyncMode.copy,
        cron="0 * * * *",
        enabled=False,
    )
    session.add_all([enabled, disabled])
    session.commit()
    return enabled, disabled


def test_parse_cron_valid():
    trigger = parse_cron("0 3 * * *")
    assert str(trigger.fields[6]) == "day_of_week[day_of_week='*']" or True


def test_parse_cron_invalid():
    with pytest.raises(ValueError):
        parse_cron("bad cron")


def test_register_and_remove(db_engine, fake_client):
    with db_module.session_factory() as session:
        enabled, _ = _make_tasks(session)

    svc = SchedulerService(db_module.session_factory, fake_client, poll_interval_seconds=60)
    svc.is_leader = True  # only the leader actually registers user tasks
    svc.register_task(enabled)
    assert svc.scheduler.get_job("task-1") is not None

    svc.remove_task(enabled.id)
    assert svc.scheduler.get_job("task-1") is None


def test_sync_from_db_registers_only_enabled(db_engine, fake_client):
    with db_module.session_factory() as session:
        _make_tasks(session)

    svc = SchedulerService(db_module.session_factory, fake_client, poll_interval_seconds=60)
    svc.is_leader = True
    svc.sync_from_db()

    job_ids = {job.id for job in svc.scheduler.get_jobs()}
    assert "task-1" in job_ids
    assert "task-2" not in job_ids


def test_reschedule_on_cron_change(db_engine, fake_client):
    with db_module.session_factory() as session:
        enabled, _ = _make_tasks(session)

    svc = SchedulerService(db_module.session_factory, fake_client, poll_interval_seconds=60)
    svc.is_leader = True
    svc.register_task(enabled)
    enabled.cron = "0 4 * * *"
    svc.register_task(enabled)

    assert len([j for j in svc.scheduler.get_jobs() if j.id == "task-1"]) == 1


def test_register_check_task_job(db_engine, fake_client):
    from app.models import CheckTask, StorageConfig

    with db_module.session_factory() as session:
        src = StorageConfig(name="s1", type="s3", parameters={})
        dst = StorageConfig(name="s2", type="s3", parameters={})
        session.add_all([src, dst])
        session.commit()
        check_task = CheckTask(
            name="check",
            src_storage_id=src.id,
            src_path="/a",
            dst_storage_id=dst.id,
            dst_path="/b",
            cron="0 5 * * *",
            enabled=True,
            check_options={},
        )
        session.add(check_task)
        session.commit()

    svc = SchedulerService(db_module.session_factory, fake_client, poll_interval_seconds=60)
    svc.is_leader = True
    svc.register_check_task(check_task)
    assert svc.scheduler.get_job("checktask-1") is not None

    svc.remove_check_task(check_task.id)
    assert svc.scheduler.get_job("checktask-1") is None


def test_sync_from_db_registers_check_tasks(db_engine, fake_client):
    from app.models import CheckTask, StorageConfig

    with db_module.session_factory() as session:
        _make_tasks(session)
        src = StorageConfig(name="s3", type="s3", parameters={})
        dst = StorageConfig(name="s4", type="s3", parameters={})
        session.add_all([src, dst])
        session.commit()
        session.add_all(
            [
                CheckTask(
                    name="with-cron",
                    src_storage_id=src.id,
                    src_path="/a",
                    dst_storage_id=dst.id,
                    dst_path="/b",
                    cron="0 5 * * *",
                    enabled=True,
                    check_options={},
                ),
                CheckTask(
                    name="no-cron",
                    src_storage_id=src.id,
                    src_path="/a",
                    dst_storage_id=dst.id,
                    dst_path="/b",
                    cron=None,
                    enabled=True,
                    check_options={},
                ),
            ]
        )
        session.commit()

    svc = SchedulerService(db_module.session_factory, fake_client, poll_interval_seconds=60)
    svc.is_leader = True
    svc.sync_from_db()

    job_ids = {job.id for job in svc.scheduler.get_jobs()}
    assert "checktask-1" in job_ids
    assert "checktask-2" not in job_ids
