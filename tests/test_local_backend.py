"""Tests for the local-FS backend (rclone ``type=local``) wiring."""
from __future__ import annotations

import pytest

import app.db as db_module
from app.api.storage_sources import build_remote_parameters
from app.auth.security import hash_password
from app.models import (
    DataSource,
    StorageSource,
    User,
    UserRole,
)


# --- helpers ---------------------------------------------------------------


def _admin(session) -> User:
    u = User(
        username="admin", password_hash=hash_password("p" * 12),
        role=UserRole.admin,
    )
    session.add(u)
    session.commit()
    session.refresh(u)
    return u


# --- build_remote_parameters: AK/SK injection -----------------------------


def test_local_does_not_inject_credentials(db_engine):
    """rclone's ``local`` backend ignores AK/SK; verify we don't leak
    whatever junk sits on the DataSource into the rcd config."""
    session = db_module.session_factory()
    src = StorageSource(
        name="local-data", type="local",
        extra={"root": "/var/data"},
    )
    session.add(src)
    session.commit()
    session.refresh(src)
    admin = _admin(session)
    ds = DataSource(
        name="ds", storage_source_id=src.id, path="/some/subdir",
        # Even if AK/SK accidentally contain values, local must not push them.
        access_key_id="LEAKED-AK", secret_access_key="LEAKED-SK",
        owner_user_id=admin.id,
    )
    session.add(ds)
    session.commit()

    params = build_remote_parameters(src, ds)
    assert "access_key_id" not in params
    assert "secret_access_key" not in params
    # Provider is s3-specific; local must not emit it either.
    assert "provider" not in params
    # extra.root is forwarded verbatim — admin scoping still works.
    assert params.get("root") == "/var/data"


def test_local_defaults_root_to_filesystem(db_engine):
    """When admin doesn't set ``extra.root`` we must default to ``/`` —
    otherwise rclone interprets the remote as the rcd's CWD and absolute
    ``ds.path`` resolves to ``<cwd>/var/data`` which 404s.

    Without this default, the verify endpoint fails with HTTP 404 on
    otherwise-correct local data sources.
    """
    session = db_module.session_factory()
    src = StorageSource(name="local-data", type="local")  # no extra
    session.add(src)
    session.commit()
    session.refresh(src)
    admin = _admin(session)
    ds = DataSource(
        name="ds", storage_source_id=src.id, path="/var/data",
        owner_user_id=admin.id,
    )
    session.add(ds)
    session.commit()

    params = build_remote_parameters(src, ds)
    assert params["root"] == "/", (
        "admin who didn't set extra.root must still get a usable rclone "
        "remote — without this, ``ds.path`` resolves to ``<CWD>/<path>`` "
        "and rclone rc returns 404"
    )


def test_local_admin_root_overrides_default(db_engine):
    """Admin-set ``extra.root`` wins over the default."""
    session = db_module.session_factory()
    src = StorageSource(name="local-data", type="local",
                        extra={"root": "/srv/data"})
    session.add(src)
    session.commit()
    session.refresh(src)
    admin = _admin(session)
    ds = DataSource(
        name="ds", storage_source_id=src.id, path="/sub",
        owner_user_id=admin.id,
    )
    session.add(ds)
    session.commit()

    params = build_remote_parameters(src, ds)
    assert params["root"] == "/srv/data"


def test_s3_still_injects_credentials(db_engine):
    """Non-local sources keep emitting AK/SK — the v3 path didn't regress
    the S3 / OSS / COS etc. flow."""
    session = db_module.session_factory()
    src = StorageSource(
        name="s3", type="s3", endpoint="http://s3",
        region="us-east-1",
    )
    session.add(src)
    session.commit()
    session.refresh(src)
    admin = _admin(session)
    ds = DataSource(
        name="ds", storage_source_id=src.id, path="bucket",
        access_key_id="AK", secret_access_key="SK",
        owner_user_id=admin.id,
    )
    session.add(ds)
    session.commit()

    params = build_remote_parameters(src, ds)
    assert params["access_key_id"] == "AK"
    assert params["secret_access_key"] == "SK"
    # S3 gets a default provider when none is set in extra.
    assert params["provider"] == "Other"


# --- API: AK/SK required iff non-local -----------------------------------


def test_local_data_source_creation_no_credentials(authed_app_client):
    r = authed_app_client.post(
        "/api/storage-sources",
        json={"name": "local-src", "type": "local"},
    )
    assert r.status_code == 201, r.text
    src_id = r.json()["id"]

    # Data source WITHOUT AK/SK on a local source → 201 OK.
    r = authed_app_client.post(
        "/api/data-sources",
        json={
            "name": "ds-local",
            "storage_source_id": src_id,
            "path": "/tmp/local-data",
            # explicitly omitted: access_key_id, secret_access_key
        },
    )
    assert r.status_code == 201, r.text
    body = r.json()
    assert body["access_key_id"] is None
    assert body["secret_access_key"] is None


def test_s3_data_source_creation_requires_credentials(authed_app_client):
    r = authed_app_client.post(
        "/api/storage-sources",
        json={"name": "s3-src", "type": "s3", "endpoint": "http://s3"},
    )
    assert r.status_code == 201
    src_id = r.json()["id"]

    # Data source on s3 WITHOUT AK/SK → 400.
    r = authed_app_client.post(
        "/api/data-sources",
        json={
            "name": "ds-no-creds",
            "storage_source_id": src_id,
            "path": "bucket",
        },
    )
    assert r.status_code == 400, r.text
    assert "access_key_id" in r.json()["detail"]


def test_s3_data_source_empty_ak_sk_rejected(authed_app_client):
    """Empty strings must NOT slip past the cross-field check — backend
    treats them as missing."""
    r = authed_app_client.post(
        "/api/storage-sources",
        json={"name": "s3-src2", "type": "s3", "endpoint": "http://s3"},
    )
    assert r.status_code == 201
    src_id = r.json()["id"]
    r = authed_app_client.post(
        "/api/data-sources",
        json={
            "name": "ds-empty",
            "storage_source_id": src_id,
            "path": "bucket",
            "access_key_id": "",
            "secret_access_key": "",
        },
    )
    assert r.status_code == 400


# --- v3 migration: legacy type=local row → DataSource with NULL AK/SK --


def test_migrate_legacy_local_row(db_engine):
    """StorageConfig(type='local', parameters={...}) → DataSource with NULL
    access_key_id / secret_access_key (not empty strings)."""
    from sqlalchemy import text
    from app.services.migrate_storage import migrate_storage_configs

    session = db_module.session_factory()
    _admin(session)

    # Seed a legacy local StorageConfig manually (no parameters since local
    # doesn't carry any).
    from app.models import StorageConfig
    cfg = StorageConfig(
        name="legacy-local", type="local", parameters={"path": "/var/data"},
    )
    session.add(cfg)
    session.commit()
    session.refresh(cfg)

    counts = migrate_storage_configs(db_module.session_factory)
    assert counts["sources_created"] == 1
    assert counts["datasources_created"] == 1

    session = db_module.session_factory()
    srcs = session.scalars(
        __import__("sqlalchemy").select(StorageSource)
    ).all()
    assert len(srcs) == 1
    assert srcs[0].type == "local"

    ds = session.scalars(__import__("sqlalchemy").select(DataSource)).first()
    assert ds.access_key_id is None
    assert ds.secret_access_key is None
    assert ds.path == "/var/data"
