from app.models import RunStatus, RunTrigger, StorageConfig, SyncMode, SyncRun, SyncTask


def test_models_relations(db_engine):
    import app.db as db_module

    with db_module.session_factory() as session:
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
            rclone_options={"transfers": 4},
        )
        session.add(task)
        session.commit()

        run = SyncRun(
            task_id=task.id, job_id=1, status=RunStatus.running, trigger=RunTrigger.schedule
        )
        session.add(run)
        session.commit()

        assert task.src_storage.name == "local-s3"
        assert task.dst_storage.name == "remote-s3"
        assert task.runs[0].id == run.id
        assert run.task.name == "backup"
