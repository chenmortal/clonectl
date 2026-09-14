"""Tests for the startup migration StorageConfig → StorageSource + DataSource."""
from __future__ import annotations

import pytest
from sqlalchemy import select

import app.db as db_module
from app.auth.security import hash_password
from app.models import (
    CheckTask,
    DataSource,
    MigrationLog,
    StorageConfig,
    StorageSource,
    SyncMode,
    SyncTask,
    User,
    UserRole,
)
from app.services.migrate_storage import migrate_storage_configs


def _make_admin(session) -> User:
    user = User(
        username="admin",
        password_hash=hash_password("p" * 12),
        role=UserRole.admin,
    )
    session.add(user)
    session.commit()
    session.refresh(user)
    return user


def _seed_legacy(session) -> tuple[StorageConfig, StorageConfig]:
    a = StorageConfig(
        name="legacy-a", type="s3",
        parameters={
            "endpoint": "http://s3-a", "region": "us-east-1",
            "access_key_id": "AK-A", "secret_access_key": "SK-A", "path": "bucket-a",
            "provider": "Minio",
        },
    )
    b = StorageConfig(
        name="legacy-b", type="oss",
        parameters={
            "endpoint": "http://oss-b",
            "access_key_id": "AK-B", "secret_access_key": "SK-B", "path": "bucket-b",
        },
    )
    session.add_all([a, b])
    session.commit()
    session.refresh(a)
    session.refresh(b)
    return a, b


def test_migrate_creates_one_source_and_one_ds_per_legacy_row(db_engine):
    _make_admin(db_module.session_factory())
    session = db_module.session_factory()
    a, b = _seed_legacy(session)
    counts = migrate_storage_configs(db_module.session_factory)
    assert counts["sources_created"] == 2
    assert counts["datasources_created"] == 2
    assert counts["skipped_already_migrated"] == 0

    session = db_module.session_factory()
    srcs = session.scalars(select(StorageSource)).all()
    dss = session.scalars(select(DataSource)).all()
    logs = session.scalars(select(MigrationLog)).all()
    assert len(srcs) == 2
    assert len(dss) == 2
    assert len(logs) == 2

    by_name = {s.name: s for s in srcs}
    assert by_name["legacy-a"].type == "s3"
    assert by_name["legacy-a"].endpoint == "http://s3-a"
    assert by_name["legacy-a"].region == "us-east-1"
    assert by_name["legacy-a"].extra.get("provider") == "Minio"
    assert "access_key_id" not in by_name["legacy-a"].extra

    ds_by_legacy = {l.legacy_id: l.new_id for l in logs}
    ds_a = session.get(DataSource, ds_by_legacy[a.id])
    assert ds_a.path == "bucket-a"
    assert ds_a.access_key_id == "AK-A"
    assert ds_a.secret_access_key == "SK-A"
    assert ds_a.name == "legacy-a-default"
    assert ds_a.owner_user_id is not None


def test_migrate_remaps_task_fks(db_engine):
    _make_admin(db_module.session_factory())
    session = db_module.session_factory()
    a, b = _seed_legacy(session)
    task = SyncTask(
        name="t", src_storage_id=a.id, src_path="/x",
        dst_storage_id=b.id, dst_path="/y",
        mode=SyncMode.sync, cron="0 * * * *",
    )
    session.add(task)
    session.commit()
    session.refresh(task)

    counts = migrate_storage_configs(db_module.session_factory)
    assert counts["tasks_remapped"] == 1

    session = db_module.session_factory()
    t = session.get(SyncTask, task.id)
    assert t.src_storage_id is None
    assert t.dst_storage_id is None
    assert t.src_data_source_id is not None
    assert t.dst_data_source_id is not None


def test_migrate_idempotent(db_engine):
    _make_admin(db_module.session_factory())
    _seed_legacy(db_module.session_factory())

    migrate_storage_configs(db_module.session_factory)
    counts2 = migrate_storage_configs(db_module.session_factory)
    assert counts2["skipped_already_migrated"] == 2
    assert counts2["sources_created"] == 0
    assert counts2["datasources_created"] == 0

    session = db_module.session_factory()
    assert len(session.scalars(select(StorageSource)).all()) == 2
    assert len(session.scalars(select(DataSource)).all()) == 2


def test_migrate_handles_missing_admin(db_engine):
    """No admin user — migration logs an error and skips all rows."""
    _seed_legacy(db_module.session_factory())
    counts = migrate_storage_configs(db_module.session_factory)
    assert counts["skipped_no_admin"] == 2
    assert counts["sources_created"] == 0

    session = db_module.session_factory()
    assert len(session.scalars(select(StorageSource)).all()) == 0


def test_migrate_remaps_check_tasks(db_engine):
    _make_admin(db_module.session_factory())
    session = db_module.session_factory()
    a, b = _seed_legacy(session)
    task = CheckTask(
        name="ct", src_storage_id=a.id, src_path="/x",
        dst_storage_id=b.id, dst_path="/y",
    )
    session.add(task)
    session.commit()
    counts = migrate_storage_configs(db_module.session_factory)
    assert counts["checks_remapped"] == 1

    session = db_module.session_factory()
    t = session.get(CheckTask, task.id)
    assert t.src_data_source_id is not None
    assert t.src_storage_id is None


def test_migrate_handles_not_null_storage_fks(db_engine):
    """Existing dev DBs have sync_tasks.src_storage_id declared NOT NULL.

    SQLite has no ALTER COLUMN DROP NOT NULL, so init_db() must rebuild the
    table with the column relaxed before the migration can NULL it out.
    """
    from sqlalchemy import text

    _make_admin(db_module.session_factory())
    session = db_module.session_factory()
    a, b = _seed_legacy(session)

    # Capture the v2 sync_tasks column list (already created by Base.metadata
    # in conftest), then rebuild without those new columns and with
    # src_storage_id / dst_storage_id back to NOT NULL — the "old DB" shape.
    with db_module.engine.connect() as conn:
        cols = [r[1] for r in conn.execute(text("PRAGMA table_info(sync_tasks)")).fetchall()]

    cols_def = {
        "id": "INTEGER PRIMARY KEY",
        "name": "VARCHAR(128) NOT NULL",
        "src_storage_id": "INTEGER NOT NULL REFERENCES storage_configs(id)",
        "src_path": "VARCHAR(512) NOT NULL",
        "dst_storage_id": "INTEGER NOT NULL REFERENCES storage_configs(id)",
        "src_data_source_id": "INTEGER REFERENCES data_sources(id)",
        "dst_data_source_id": "INTEGER REFERENCES data_sources(id)",
        "dst_path": "VARCHAR(512) NOT NULL",
        "mode": "VARCHAR NOT NULL",
        "cron": "VARCHAR(128) NOT NULL",
        "enabled": "BOOLEAN NOT NULL DEFAULT 1",
        "rclone_options": "JSON NOT NULL DEFAULT '{}'",
        "pre_check_task_id": "INTEGER REFERENCES check_tasks(id)",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    }
    selected_cols = [c for c in cols if c in cols_def]
    new_cols_sql = ", ".join(f"{c} {cols_def[c]}" for c in selected_cols)
    with db_module.engine.begin() as conn:
        conn.execute(text(f"CREATE TABLE sync_tasks__new ({new_cols_sql})"))
        conn.execute(
            text(f"INSERT INTO sync_tasks__new ({', '.join(selected_cols)}) "
                 f"SELECT {', '.join(selected_cols)} FROM sync_tasks")
        )
        conn.execute(text("DROP TABLE sync_tasks"))
        conn.execute(text("ALTER TABLE sync_tasks__new RENAME TO sync_tasks"))

    # Sanity: src_storage_id is NOT NULL again.
    with db_module.engine.connect() as conn:
        rows = conn.execute(text("PRAGMA table_info(sync_tasks)")).fetchall()
        notnull = {r[1]: r[3] for r in rows}
        assert notnull["src_storage_id"] == 1

    # Seed a task against the NOT-NULL table; without the rebuild path this
    # UPDATE in migrate_storage_configs would fail with NOT NULL constraint.
    task = SyncTask(
        name="t", src_storage_id=a.id, src_path="/x",
        dst_storage_id=b.id, dst_path="/y",
        mode=SyncMode.sync, cron="0 * * * *",
    )
    session.add(task)
    session.commit()

    db_module.init_db()
    counts = migrate_storage_configs(db_module.session_factory)
    assert counts["tasks_remapped"] == 1

    session = db_module.session_factory()
    t = session.get(SyncTask, task.id)
    assert t.src_storage_id is None
    assert t.src_data_source_id is not None
    assert t.dst_storage_id is None
    assert t.dst_data_source_id is not None
