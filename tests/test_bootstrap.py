"""Tests for the env-driven admin bootstrap."""
from __future__ import annotations

from sqlalchemy import func, select

import app.db as db_module
from app.auth.bootstrap import bootstrap_admin
from app.config import Settings
from app.models import User, UserRole


class _StubSettings(Settings):
    """Settings subclass with deterministic test values; avoids reading .env."""

    def __init__(self, admin_user: str = "", admin_password: str = ""):
        super().__init__()
        # override the two bootstrap fields after super init
        object.__setattr__(self, "bootstrap_admin_user", admin_user)
        object.__setattr__(self, "bootstrap_admin_password", admin_password)


def test_bootstrap_creates_when_users_empty_and_env_set(db_engine):
    settings = _StubSettings(admin_user="root", admin_password="root-pass-1234")
    bootstrap_admin(db_module.session_factory, settings)
    with db_module.session_factory() as s:
        users = s.scalars(select(User)).all()
        assert len(users) == 1
        assert users[0].username == "root"
        assert users[0].role == UserRole.admin


def test_bootstrap_noop_when_users_present(db_engine, admin_user):
    settings = _StubSettings(admin_user="different-user", admin_password="new-pass-1234")
    bootstrap_admin(db_module.session_factory, settings)
    with db_module.session_factory() as s:
        users = s.scalars(select(User)).all()
        # Only the admin_user fixture's user; no second user from bootstrap.
        assert len(users) == 1
        assert users[0].id == admin_user["id"]


def test_bootstrap_noop_when_user_missing(db_engine):
    settings = _StubSettings(admin_user="", admin_password="root-pass-1234")
    bootstrap_admin(db_module.session_factory, settings)
    with db_module.session_factory() as s:
        count = s.scalar(select(func.count()).select_from(User)) or 0
        assert count == 0


def test_bootstrap_noop_when_password_missing(db_engine):
    settings = _StubSettings(admin_user="root", admin_password="")
    bootstrap_admin(db_module.session_factory, settings)
    with db_module.session_factory() as s:
        count = s.scalar(select(func.count()).select_from(User)) or 0
        assert count == 0


def test_bootstrap_is_idempotent(db_engine):
    settings = _StubSettings(admin_user="root", admin_password="root-pass-1234")
    bootstrap_admin(db_module.session_factory, settings)
    bootstrap_admin(db_module.session_factory, settings)  # second call
    bootstrap_admin(db_module.session_factory, settings)  # third call
    with db_module.session_factory() as s:
        count = s.scalar(select(func.count()).select_from(User)) or 0
        assert count == 1


def test_login_updates_last_login_at(authed_app_client, db_engine, admin_user):
    import datetime as _dt

    from app.models import User

    with db_module.session_factory() as s:
        u = s.get(User, admin_user["id"])
        assert u.last_login_at is None

    resp = authed_app_client.post(
        "/api/auth/login",
        json={"username": admin_user["username"], "password": admin_user["password"]},
    )
    assert resp.status_code == 200

    with db_module.session_factory() as s:
        u = s.get(User, admin_user["id"])
        assert u.last_login_at is not None
        delta = _dt.datetime.utcnow() - u.last_login_at
        assert delta.total_seconds() < 10
