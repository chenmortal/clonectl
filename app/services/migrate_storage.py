"""One-shot startup migration: StorageConfig → StorageSource + DataSource.

Idempotent. Safe to run on every startup. Returns counts for logging.

Mapping (1:1, no fan-out):
  StorageConfig {name, type, parameters:{endpoint, region, AK, SK, path, ...}}
  → StorageSource {name=cfg.name, type=cfg.type, endpoint=p.endpoint,
                    region=p.region, extra=p minus secrets/path}
  → DataSource {name=cfg.name + "-default", storage_source_id=src.id,
                path=p.path or "/", AK=..., SK=...,
                owner_user_id=earliest admin user}

Task FK backfill (SyncTask.src_storage_id/dst_storage_id and the same on
CheckTask) is performed in the same transaction. Old FKs are NULLed out
after the new FKs are populated so legacy columns retain history but stop
referencing migrated rows.

Old StorageConfig rows and their rclone rcd remote names are kept
(read-only fallback) until the next major release drops them.
"""
from __future__ import annotations

import logging
from typing import Any

from sqlalchemy import select
from sqlalchemy.orm import Session, sessionmaker

from app.models import (
    CheckTask,
    MigrationLog,
    StorageConfig,
    StorageSource,
    DataSource,
    SyncTask,
    User,
    UserRole,
)

logger = logging.getLogger(__name__)


# Keys that belong to the storage source (not the data source) and shouldn't
# be duplicated into the data source's secret columns.
_SOURCE_ONLY_KEYS = {"endpoint", "region", "name"}
# Keys that are secrets and should be removed from extra.
_SECRET_KEYS = {"access_key_id", "secret_access_key", "token"}


def _split_parameters(parameters: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any]]:
    """Return ``(extra_dict, ds_secrets_dict)``.

    ``extra_dict`` is what goes on StorageSource.extra: non-secret,
    non-region/non-endpoint/non-name provider params (e.g. ``provider``,
    ``no_check_bucket``).

    ``ds_secrets_dict`` is what the DataSource needs:
    ``{access_key_id, secret_access_key, path}``. Missing keys yield empty
    values; the caller fills defaults.
    """
    extra: dict[str, Any] = {}
    secrets: dict[str, Any] = {}
    for key, value in (parameters or {}).items():
        if key in _SOURCE_ONLY_KEYS:
            continue
        if key in _SECRET_KEYS:
            secrets[key] = value
            continue
        extra[key] = value
    return extra, secrets


def _pick_owner(session: Session) -> int | None:
    """Return the lowest-id admin user; ``None`` if no admin exists."""
    return session.scalar(
        select(User.id).where(User.role == UserRole.admin).order_by(User.id).limit(1)
    )


def migrate_storage_configs(session_factory: sessionmaker) -> dict[str, int]:
    """Migrate every legacy StorageConfig row to source + ds. Idempotent.

    Returns a counts dict suitable for logging.
    """
    counts = {
        "sources_created": 0,
        "datasources_created": 0,
        "tasks_remapped": 0,
        "checks_remapped": 0,
        "skipped_already_migrated": 0,
        "skipped_no_admin": 0,
        "orphaned_tasks_skipped": 0,
    }
    with session_factory() as session:
        owner_id = _pick_owner(session)
        if owner_id is None:
            # Refuse to migrate without an admin — DataSource requires
            # owner_user_id NOT NULL. Caller logs and continues.
            logger.error(
                "storage migration skipped: no admin user to own migrated data sources"
            )
            counts["skipped_no_admin"] = len(
                session.scalars(select(StorageConfig)).all()
            )
            return counts

        # 1. Migrate StorageConfig rows
        for cfg in session.scalars(select(StorageConfig)).all():
            existing = session.get(MigrationLog, ("storage_config", cfg.id))
            if existing is not None:
                counts["skipped_already_migrated"] += 1
                continue

            params = cfg.parameters or {}
            extra, secrets = _split_parameters(params)

            src = StorageSource(
                name=cfg.name,
                type=cfg.type,
                endpoint=params.get("endpoint"),
                region=params.get("region"),
                extra=extra,
            )
            session.add(src)
            session.flush()  # populate src.id

            ds = DataSource(
                name=f"{cfg.name}-default",
                storage_source_id=src.id,
                path=params.get("path", "/"),
                # Use ``None`` instead of ``""`` so legacy ``type="local"``
                # rows (which never carried AK/SK) come through with NULL —
                # the v3 schema relaxed NOT NULL on these columns
                # specifically for that.
                access_key_id=secrets.get("access_key_id") or None,
                secret_access_key=secrets.get("secret_access_key") or None,
                description=f"migrated from storage_config {cfg.id}",
                owner_user_id=owner_id,
            )
            session.add(ds)
            session.flush()  # populate ds.id

            session.add(
                MigrationLog(
                    table_name="storage_config", legacy_id=cfg.id, new_id=ds.id,
                )
            )
            counts["sources_created"] += 1
            counts["datasources_created"] += 1

        # 2. Backfill task FKs
        for task in session.scalars(select(SyncTask)).all():
            counts["tasks_remapped"] += _remap_task_refs(session, task, "sync_tasks")
        for task in session.scalars(select(CheckTask)).all():
            counts["checks_remapped"] += _remap_task_refs(session, task, "check_tasks")

        session.commit()
    return counts


def _remap_task_refs(session: Session, task: Any, table_name: str) -> int:
    """Migrate a single task's src/dst storage FKs to data_source FKs.

    Returns 1 if any FK was remapped, 0 otherwise. Orphaned (no migration
    log entry) tasks are skipped with a WARNING.
    """
    changed = 0
    for side in ("src", "dst"):
        old_id = getattr(task, f"{side}_storage_id")
        new_id = getattr(task, f"{side}_data_source_id")
        if old_id is None:
            continue
        if new_id is not None:
            # Already migrated (idempotent)
            continue
        log = session.get(MigrationLog, ("storage_config", old_id))
        if log is None:
            logger.warning(
                "%s id=%s %s_storage_id=%s has no migration log; skipping",
                table_name, task.id, side, old_id,
            )
            continue
        setattr(task, f"{side}_data_source_id", log.new_id)
        setattr(task, f"{side}_storage_id", None)
        changed = 1
    return changed
