import logging

from sqlalchemy import select
from sqlalchemy.orm import Session, sessionmaker

from app.api.storage_sources import _ds_remote_name, build_remote_parameters
from app.models import DataSource, StorageConfig
from app.rclone.client import RcloneApiError, RcloneClient

logger = logging.getLogger(__name__)


def ensure_remote(session: Session, client: RcloneClient, storage: StorageConfig) -> None:
    """Push the legacy ``StorageConfig`` row to rclone rcd.

    New code uses :func:`ensure_data_source_remote` instead. Kept for the
    deprecation window so any leftover references resolve cleanly.
    """
    try:
        client.create_remote(storage.name, storage.type, storage.parameters)
    except RcloneApiError as exc:
        body = exc.body if isinstance(exc.body, dict) else {}
        message = str(body.get("error", exc))
        if "already exists" not in message:
            raise


def ensure_data_source_remote(
    session: Session, client: RcloneClient, ds: DataSource
) -> None:
    """Push the (source + ds) pair as a remote named ``ds-{id}``.

    Idempotent: swallows "already exists" errors. Logs and re-raises
    other failures so the caller can decide.
    """
    name = _ds_remote_name(ds)
    src = ds.storage_source
    params = build_remote_parameters(src, ds)
    try:
        client.create_remote(name, src.type, params)
    except RcloneApiError as exc:
        body = exc.body if isinstance(exc.body, dict) else {}
        message = str(body.get("error", exc))
        if "already exists" not in message:
            raise


def remove_remote(client: RcloneClient, name: str) -> None:
    try:
        client.delete_remote(name)
    except RcloneApiError as exc:
        body = exc.body if isinstance(exc.body, dict) else {}
        message = str(body.get("error", exc)).lower()
        if "find section" not in message and "not found" not in message:
            raise


def sync_all_remotes(session_factory: sessionmaker, client: RcloneClient) -> tuple[int, int]:
    """Push every StorageConfig row to rclone rcd.

    DataSources are NOT pushed here — they're pushed on demand by the
    runner (before each sync) and on /verify, so we don't pay the cost
    at startup for unused ones.
    """
    synced, failed = 0, 0
    with session_factory() as session:
        storages = session.scalars(select(StorageConfig)).all()
        for storage in storages:
            try:
                ensure_remote(session, client, storage)
                synced += 1
            except RcloneApiError:
                failed += 1
                logger.exception("failed to push storage %s to rclone rcd", storage.name)
    return synced, failed
