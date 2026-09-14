"""Tests for StorageSource + DataSource APIs (CRUD, RBAC, verify)."""
from __future__ import annotations

import pytest

from app import models
from app.auth.security import hash_password
from app.models import (
    BindingPermission,
    DataSource,
    DataSourceBinding,
    StorageSource,
    User,
    UserRole,
)
from app.rclone.client import RcloneApiError


# --- helpers --------------------------------------------------------------


def _create_source(session, name: str = "src", type_: str = "s3") -> StorageSource:
    src = StorageSource(
        name=name, type=type_, endpoint="http://example", region="us-east-1",
        extra={"provider": "Other"},
    )
    session.add(src)
    session.commit()
    session.refresh(src)
    return src


def _create_ds(
    session, owner_id: int, source_id: int, name: str = "ds"
) -> DataSource:
    ds = DataSource(
        name=name,
        storage_source_id=source_id,
        path="bucket/path",
        access_key_id="AK",
        secret_access_key="SK",
        owner_user_id=owner_id,
    )
    session.add(ds)
    session.commit()
    session.refresh(ds)
    return ds


def _create_user(session, role: UserRole, username: str = "u") -> User:
    u = User(
        username=username, password_hash=hash_password("p" * 12), role=role,
    )
    session.add(u)
    session.commit()
    session.refresh(u)
    return u


# --- StorageSource CRUD ---------------------------------------------------


def test_storage_source_crud(authed_app_client):
    """Admin can create / read / update / delete storage sources."""
    r = authed_app_client.post(
        "/api/storage-sources",
        json={"name": "aws-prod", "type": "s3", "endpoint": "http://s3",
              "region": "us-east-1", "extra": {"provider": "AWS"}},
    )
    assert r.status_code == 201, r.text
    src_id = r.json()["id"]
    assert r.json()["type"] == "s3"

    r = authed_app_client.get("/api/storage-sources")
    assert r.status_code == 200
    assert any(s["id"] == src_id for s in r.json())

    r = authed_app_client.put(
        f"/api/storage-sources/{src_id}", json={"region": "us-west-2"},
    )
    assert r.status_code == 200
    assert r.json()["region"] == "us-west-2"

    r = authed_app_client.delete(f"/api/storage-sources/{src_id}")
    assert r.status_code == 204


def test_storage_source_duplicate_name_409(authed_app_client):
    authed_app_client.post(
        "/api/storage-sources", json={"name": "dup", "type": "s3"},
    )
    r = authed_app_client.post(
        "/api/storage-sources", json={"name": "dup", "type": "s3"},
    )
    assert r.status_code == 409


def test_storage_source_name_pattern_rejected(authed_app_client):
    r = authed_app_client.post(
        "/api/storage-sources", json={"name": "bad name!", "type": "s3"},
    )
    assert r.status_code == 422


def test_storage_source_delete_when_used_409(authed_app_client, session):
    src = _create_source(session)
    _create_ds(session, owner_id=1, source_id=src.id)
    r = authed_app_client.delete(f"/api/storage-sources/{src.id}")
    assert r.status_code == 409


# --- DataSource CRUD + binding visibility --------------------------------


def test_data_source_create_requires_storage_source(authed_app_client):
    r = authed_app_client.post(
        "/api/data-sources",
        json={"name": "x", "storage_source_id": 999, "path": "p",
              "access_key_id": "a", "secret_access_key": "s"},
    )
    assert r.status_code == 400


def test_data_source_create_and_list(authed_app_client, session):
    src = _create_source(session)
    r = authed_app_client.post(
        "/api/data-sources",
        json={"name": "my-bucket", "storage_source_id": src.id,
              "path": "b/p", "access_key_id": "a", "secret_access_key": "s",
              "description": "demo"},
    )
    assert r.status_code == 201, r.text
    body = r.json()
    assert body["name"] == "my-bucket"
    assert body["secret_access_key"] == "s"  # server returns in full; UI redacts

    r = authed_app_client.get("/api/data-sources")
    assert r.status_code == 200
    assert len(r.json()) == 1


def test_data_source_unique_name_per_owner(authed_app_client, session):
    src = _create_source(session)
    # First create OK
    r = authed_app_client.post(
        "/api/data-sources",
        json={"name": "same", "storage_source_id": src.id, "path": "p",
              "access_key_id": "a", "secret_access_key": "s"},
    )
    assert r.status_code == 201
    # Same name, same owner → 409
    r = authed_app_client.post(
        "/api/data-sources",
        json={"name": "same", "storage_source_id": src.id, "path": "p2",
              "access_key_id": "a", "secret_access_key": "s"},
    )
    assert r.status_code == 409


# --- Verify endpoint ------------------------------------------------------


def test_verify_data_source_success(authed_app_client, session):
    src = _create_source(session)
    ds = _create_ds(session, owner_id=1, source_id=src.id)
    r = authed_app_client.post(f"/api/data-sources/{ds.id}/verify")
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["read_ok"] is True
    assert body["write_ok"] is True
    assert body["error"] is None


def test_verify_data_source_read_fails(authed_app_client, session, fake_client):
    src = _create_source(session)
    ds = _create_ds(session, owner_id=1, source_id=src.id)
    fake_client.list_fail = RcloneApiError("creds denied", 403, {"error": "creds"})
    r = authed_app_client.post(f"/api/data-sources/{ds.id}/verify")
    assert r.status_code == 200
    body = r.json()
    assert body["read_ok"] is False
    assert "creds" in body["error"]


def test_verify_data_source_write_fails(authed_app_client, session, fake_client):
    src = _create_source(session)
    ds = _create_ds(session, owner_id=1, source_id=src.id)
    fake_client.write_fail = RcloneApiError("write denied", 403, {"error": "denied"})
    r = authed_app_client.post(f"/api/data-sources/{ds.id}/verify")
    body = r.json()
    assert body["read_ok"] is True
    assert body["write_ok"] is False
    assert "denied" in body["error"]


def test_verify_updates_last_verified(authed_app_client, session):
    src = _create_source(session)
    ds = _create_ds(session, owner_id=1, source_id=src.id)
    assert ds.last_verified_at is None
    r = authed_app_client.post(f"/api/data-sources/{ds.id}/verify")
    assert r.status_code == 200
    r2 = authed_app_client.get(f"/api/data-sources/{ds.id}")
    assert r2.json()["last_verified_at"] is not None


# --- RBAC: view/edit/admin + bindings ------------------------------------


@pytest.fixture()
def extra_users(session):
    """Add two non-admin users; return dict with their ids."""
    u1 = _create_user(session, UserRole.view, "view-user")
    u2 = _create_user(session, UserRole.edit, "edit-user")
    return {"view_id": u1.id, "edit_id": u2.id}


def test_view_role_cannot_create_data_source(app_client, session):
    """A view-role user gets 403 on POST /api/data-sources."""
    src = _create_source(session)
    viewer = _create_user(session, UserRole.view, "viewer-x")
    from app.auth.tokens import create_access_token

    tok = create_access_token(
        user_id=viewer.id, username="viewer-x", role="view",
    )
    r = app_client.post(
        "/api/data-sources",
        headers={"Authorization": f"Bearer {tok}"},
        json={"name": "x", "storage_source_id": src.id, "path": "p",
              "access_key_id": "a", "secret_access_key": "s"},
    )
    assert r.status_code == 403


def test_list_data_sources_filters_by_binding(app_client, session, extra_users):
    """Non-admin only sees owned + bound rows."""
    src = _create_source(session)
    # admin owns one DS
    ds_admin = _create_ds(session, owner_id=1, source_id=src.id, name="admin-ds")
    # an "other" owner (admin's other identity for the sake of testing)
    other = _create_user(session, UserRole.edit, "other")
    ds_other = _create_ds(
        session, owner_id=other.id, source_id=src.id, name="other-ds"
    )

    # Bind view-user to admin-ds with read
    session.add(DataSourceBinding(
        data_source_id=ds_admin.id, user_id=extra_users["view_id"],
        permission=BindingPermission.read,
    ))
    session.commit()

    # Log in as view-user
    from app.auth.tokens import create_access_token
    tok = create_access_token(
        user_id=extra_users["view_id"], username="u", role="view",
    )
    r = app_client.get(
        "/api/data-sources",
        headers={"Authorization": f"Bearer {tok}"},
    )
    assert r.status_code == 200
    ids = [d["id"] for d in r.json()]
    # view-user should only see the bound admin-ds, not other-ds
    assert ds_admin.id in ids
    assert ds_other.id not in ids


def test_binding_creates_and_lists(authed_app_client, session, extra_users):
    src = _create_source(session)
    ds = _create_ds(session, owner_id=1, source_id=src.id)
    r = authed_app_client.post(
        f"/api/data-sources/{ds.id}/bindings",
        json={"user_id": extra_users["view_id"], "permission": "read"},
    )
    assert r.status_code == 201, r.text
    bid = r.json()["id"]
    assert r.json()["permission"] == "read"

    r = authed_app_client.get(f"/api/data-sources/{ds.id}/bindings")
    assert r.status_code == 200
    assert any(b["id"] == bid for b in r.json())


def test_binding_update_and_delete(authed_app_client, session, extra_users):
    src = _create_source(session)
    ds = _create_ds(session, owner_id=1, source_id=src.id)
    r = authed_app_client.post(
        f"/api/data-sources/{ds.id}/bindings",
        json={"user_id": extra_users["view_id"], "permission": "read"},
    )
    bid = r.json()["id"]
    r = authed_app_client.put(
        f"/api/data-sources/{ds.id}/bindings/{bid}",
        json={"permission": "write"},
    )
    assert r.status_code == 200
    assert r.json()["permission"] == "write"

    r = authed_app_client.delete(f"/api/data-sources/{ds.id}/bindings/{bid}")
    assert r.status_code == 204
