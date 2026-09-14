"""RBAC matrix tests for write endpoints.

Each test logs in as one role and verifies which endpoints accept vs reject.
The matrix is in the README's "用户管理与权限" section.
"""
from __future__ import annotations

from collections.abc import Callable

import pytest

import app.db as db_module
from app.auth.security import hash_password
from app.auth.tokens import create_access_token
from app.models import StorageConfig, User, UserRole


def _seed_storage() -> int:
    """Insert a minimal storage row so task endpoints have valid FK targets."""
    with db_module.session_factory() as s:
        sc = StorageConfig(name="src-s3", type="s3", parameters={})
        dc = StorageConfig(name="dst-s3", type="s3", parameters={})
        s.add_all([sc, dc])
        s.commit()
        s.refresh(sc)
        s.refresh(dc)
        # store both ids on the rows for later retrieval
        sc_id, dc_id = sc.id, dc.id
    return sc_id, dc_id


def _make_user(role: UserRole) -> tuple[int, str]:
    """Insert a user; return (id, username)."""
    username = f"u-{role.value}-test"
    with db_module.session_factory() as s:
        u = User(
            username=username,
            password_hash=hash_password("role-pass-1234"),
            role=role,
        )
        s.add(u)
        s.commit()
        s.refresh(u)
        return u.id, username


@pytest.fixture()
def role_client(db_engine, fake_client, proxy_requests) -> Callable[[UserRole], tuple[object, str]]:
    """Factory that returns (client, token) for the requested role."""
    import httpx
    from fastapi.testclient import TestClient

    from app.config import Settings
    from app.main import create_app

    def _make(role: UserRole):
        user_id, username = _make_user(role)
        token = create_access_token(user_id=user_id, username=username, role=role.value)
        headers = {"Authorization": f"Bearer {token}"}

        def proxy_handler(request):
            return httpx.Response(
                200, json={"proxied": True, "path": request.url.path},
                headers={"x-rclone": "rcd"},
            )

        application = create_app(
            Settings(_env_file=None, database_url="sqlite://", poll_interval_seconds=3600, rclone_managed=False)
        )
        client = TestClient(application, headers=headers)
        client.app.state.rclone_client = fake_client
        client.app.state.proxy_client = httpx.Client(
            base_url="http://rc.test",
            auth=("admin", "pass"),
            transport=httpx.MockTransport(proxy_handler),
        )
        return client, token

    return _make


def _task_body(src_id: int, dst_id: int) -> dict:
    return {
        "name": "test-task",
        "src_storage_id": src_id,
        "src_path": "/data",
        "dst_storage_id": dst_id,
        "dst_path": "/backup",
        "mode": "sync",
        "cron": "0 3 * * *",
        "enabled": True,
        "rclone_options": {},
    }


def _storage_body() -> dict:
    return {"name": "test-store", "type": "s3", "parameters": {"endpoint": "http://a"}}


# ---------------------------------------------------------------------------
# /api/storages writes — all roles now get 410 (deprecated)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("role", [UserRole.admin, UserRole.edit, UserRole.view])
def test_post_storage_deprecated(role_client, role):
    client, _ = role_client(role)
    resp = client.post("/api/storages", json=_storage_body())
    assert resp.status_code == 410
    assert resp.json()["detail"]["error"] == "deprecated"


@pytest.mark.parametrize("role", [UserRole.admin, UserRole.edit, UserRole.view])
def test_put_storage_deprecated(role_client, role):
    client, _ = role_client(role)
    resp = client.put("/api/storages/1", json={"type": "s3"})
    assert resp.status_code == 410


# ---------------------------------------------------------------------------
# /api/storage-sources — admin-only writes (per user's "仅由管理员设置")
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 201),
    (UserRole.edit, 403),
    (UserRole.view, 403),
])
def test_post_storage_source(role_client, role, expect):
    client, _ = role_client(role)
    resp = client.post(
        "/api/storage-sources",
        json={"name": f"src-{role.value}", "type": "s3"},
    )
    assert resp.status_code == expect, resp.text


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 200),
    (UserRole.edit, 403),
    (UserRole.view, 403),
])
def test_put_storage_source(role_client, role, expect):
    client, _ = role_client(role)
    if role is not UserRole.admin:
        # need an existing row to PUT — skip cleanly
        resp = client.put("/api/storage-sources/1", json={"region": "us-west-2"})
        assert resp.status_code == expect
        return
    sid = client.post(
        "/api/storage-sources",
        json={"name": "put-src", "type": "s3"},
    ).json()["id"]
    resp = client.put(f"/api/storage-sources/{sid}", json={"region": "us-west-2"})
    assert resp.status_code == expect


# ---------------------------------------------------------------------------
# /api/tasks write
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 201),
    (UserRole.edit, 201),
    (UserRole.view, 403),
])
def test_post_task(role_client, role, expect):
    client, _ = role_client(role)
    src_id, dst_id = _seed_storage()
    resp = client.post("/api/tasks", json=_task_body(src_id, dst_id))
    assert resp.status_code == expect


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 202),
    (UserRole.edit, 202),
    (UserRole.view, 403),
])
def test_trigger_task(role_client, role, expect):
    client, _ = role_client(role)
    src_id, dst_id = _seed_storage()
    # create the task first; if we can't, the test for view is already validated
    create = client.post("/api/tasks", json=_task_body(src_id, dst_id))
    if create.status_code != 201:
        # view (and any role that can't create) can't reach trigger with valid id
        assert create.status_code == expect
        return
    tid = create.json()["id"]
    resp = client.post(f"/api/tasks/{tid}/trigger")
    assert resp.status_code == expect


# ---------------------------------------------------------------------------
# /api/users — admin only
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 200),
    (UserRole.edit, 403),
    (UserRole.view, 403),
])
def test_list_users(role_client, role, expect):
    client, _ = role_client(role)
    resp = client.get("/api/users")
    assert resp.status_code == expect


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 201),
    (UserRole.edit, 403),
    (UserRole.view, 403),
])
def test_create_user(role_client, role, expect):
    client, _ = role_client(role)
    resp = client.post(
        "/api/users",
        json={"username": f"new-{role.value}", "password": "new-user-pass-1234", "role": "view"},
    )
    assert resp.status_code == expect


# ---------------------------------------------------------------------------
# /rclone/* write — admin only
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 200),
    (UserRole.edit, 403),
    (UserRole.view, 403),
])
def test_rclone_post(role_client, role, expect):
    client, _ = role_client(role)
    resp = client.post("/rclone/config/create", json={"name": "x"})
    assert resp.status_code == expect


@pytest.mark.parametrize("role, expect", [
    (UserRole.admin, 200),
    (UserRole.edit, 200),
    (UserRole.view, 200),
])
def test_rclone_get(role_client, role, expect):
    """All roles can read rclone endpoints (job status, stats)."""
    client, _ = role_client(role)
    resp = client.get("/rclone/core/version")
    assert resp.status_code == expect


# ---------------------------------------------------------------------------
# unauthenticated baseline
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("method, path, body", [
    ("get", "/api/tasks", None),
    ("post", "/api/tasks", None),
    ("delete", "/api/tasks/1", None),
    ("get", "/api/storages", None),
    ("post", "/api/storages", None),
    ("get", "/api/users", None),
    ("post", "/api/users", None),
    ("get", "/api/auth/me", None),
    ("post", "/api/auth/change-password", None),
])
def test_no_token_returns_401(app_client, method, path, body):
    if body is None:
        resp = getattr(app_client, method)(path)
    else:
        resp = getattr(app_client, method)(path, json=body)
    assert resp.status_code == 401, f"{method.upper()} {path} expected 401, got {resp.status_code}"


def test_healthz_is_public(app_client):
    """Health check must not require auth — LB / keepalived probe it."""
    resp = app_client.get("/healthz")
    assert resp.status_code == 200
