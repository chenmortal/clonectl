from unittest.mock import patch

from fastapi.testclient import TestClient

import app.db as db_module
from app.config import Settings
from app.main import create_app
from app.models import StorageConfig
from app.rclone.client import RcloneApiError
from app.services.storage import sync_all_remotes


def _add_storages(session):
    session.add_all(
        [
            StorageConfig(name="s1", type="s3", parameters={"endpoint": "http://a"}),
            StorageConfig(name="s2", type="s3", parameters={"endpoint": "http://b"}),
        ]
    )
    session.commit()


def test_sync_all_remotes_pushes_everything(db_engine, fake_client):
    with db_module.session_factory() as session:
        _add_storages(session)

    synced, failed = sync_all_remotes(db_module.session_factory, fake_client)

    assert (synced, failed) == (2, 0)
    assert [r["name"] for r in fake_client.remotes] == ["s1", "s2"]


def test_sync_all_remotes_continues_on_failure(db_engine, fake_client):
    with db_module.session_factory() as session:
        _add_storages(session)

    original = fake_client.create_remote

    def flaky(name, type_, parameters):
        if name == "s1":
            raise RcloneApiError("rcd rejected s1")
        return original(name, type_, parameters)

    fake_client.create_remote = flaky

    synced, failed = sync_all_remotes(db_module.session_factory, fake_client)

    assert (synced, failed) == (1, 1)
    assert [r["name"] for r in fake_client.remotes] == ["s2"]


def test_lifespan_pushes_storages_on_startup(db_engine):
    settings = Settings(
        _env_file=None,
        database_url="sqlite://",
        poll_interval_seconds=3600,
        rclone_managed=False,
    )
    application = create_app(settings)

    with patch("app.main.sync_all_remotes", return_value=(2, 0)) as mocked:
        with TestClient(application):
            pass

    mocked.assert_called_once()
