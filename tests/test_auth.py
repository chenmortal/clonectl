"""Tests for auth/security, auth/tokens, and /api/auth/* endpoints."""
from __future__ import annotations

import time

import jwt
import pytest

from app.auth.security import hash_password, verify_password
from app.auth.tokens import (
    InvalidTokenError,
    create_access_token,
    decode_access_token,
)
from app.config import get_settings

# ---------------------------------------------------------------------------
# security
# ---------------------------------------------------------------------------


def test_hash_and_verify_password_roundtrip():
    h = hash_password("Sup3r$ecret!")
    assert h != "Sup3r$ecret!"
    assert verify_password("Sup3r$ecret!", h)
    assert not verify_password("wrong", h)


def test_verify_password_handles_garbage_hash():
    assert verify_password("anything", "not-a-bcrypt-hash") is False
    assert verify_password("anything", "") is False


# ---------------------------------------------------------------------------
# tokens
# ---------------------------------------------------------------------------


def test_create_and_decode_token():
    token = create_access_token(user_id=42, username="alice", role="edit")
    payload = decode_access_token(token)
    assert payload["sub"] == "42"
    assert payload["username"] == "alice"
    assert payload["role"] == "edit"
    assert payload["exp"] > int(time.time())


def test_decode_token_rejects_tampered_signature():
    token = create_access_token(user_id=1, username="a", role="admin")
    # flip a character in the signature segment
    head, payload, sig = token.split(".")
    tampered = f"{head}.{payload}.{sig[:-2]}AA"
    with pytest.raises(InvalidTokenError):
        decode_access_token(tampered)


def test_decode_token_rejects_wrong_secret():
    token = create_access_token(user_id=1, username="a", role="admin")
    # decode with the wrong secret
    with pytest.raises(jwt.PyJWTError):
        jwt.decode(token, "totally-different-secret", algorithms=["HS256"])


def test_decode_token_rejects_expired():
    # build an already-expired token directly
    settings = get_settings()
    expired = jwt.encode(
        {"sub": "1", "username": "a", "role": "admin",
         "iat": int(time.time()) - 7200, "exp": int(time.time()) - 60},
        settings.jwt_secret,
        algorithm=settings.jwt_algorithm,
    )
    with pytest.raises(InvalidTokenError):
        decode_access_token(expired)


# ---------------------------------------------------------------------------
# /api/auth/login
# ---------------------------------------------------------------------------


def test_login_success(authed_app_client, admin_user):
    # We already have an authed client; this confirms login works without it
    resp = authed_app_client.post(
        "/api/auth/login",
        json={"username": admin_user["username"], "password": admin_user["password"]},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["token_type"] == "bearer"
    assert body["expires_in"] > 0
    assert body["role"] == "admin"
    assert isinstance(body["access_token"], str) and len(body["access_token"]) > 20


def test_login_wrong_password_returns_401(authed_app_client, admin_user):
    resp = authed_app_client.post(
        "/api/auth/login",
        json={"username": admin_user["username"], "password": "WRONG"},
    )
    assert resp.status_code == 401


def test_login_unknown_user_returns_401(authed_app_client):
    resp = authed_app_client.post(
        "/api/auth/login",
        json={"username": "ghost", "password": "anything"},
    )
    assert resp.status_code == 401


def test_login_disabled_user_returns_401(authed_app_client, db_engine, admin_user):
    from datetime import datetime

    import app.db as db_module
    from app.models import User

    with db_module.session_factory() as s:
        u = s.get(User, admin_user["id"])
        u.disabled_at = datetime.utcnow()
        s.commit()
    resp = authed_app_client.post(
        "/api/auth/login",
        json={"username": admin_user["username"], "password": admin_user["password"]},
    )
    assert resp.status_code == 401


# ---------------------------------------------------------------------------
# /api/auth/me
# ---------------------------------------------------------------------------


def test_me_with_token(authed_app_client, admin_user):
    resp = authed_app_client.get("/api/auth/me")
    assert resp.status_code == 200
    body = resp.json()
    assert body["username"] == admin_user["username"]
    assert body["role"] == "admin"


def test_me_without_token_returns_401(app_client):
    resp = app_client.get("/api/auth/me")
    assert resp.status_code == 401
    assert resp.headers.get("www-authenticate", "").lower().startswith("bearer")


def test_me_with_garbage_token_returns_401(app_client):
    resp = app_client.get(
        "/api/auth/me", headers={"Authorization": "Bearer not-a-real-jwt"}
    )
    assert resp.status_code == 401


# ---------------------------------------------------------------------------
# /api/auth/change-password
# ---------------------------------------------------------------------------


def test_change_password_happy_path(authed_app_client, db_engine, admin_user):
    import app.db as db_module
    from app.models import User

    resp = authed_app_client.post(
        "/api/auth/change-password",
        json={"old_password": admin_user["password"], "new_password": "new-admin-pass-1234"},
    )
    assert resp.status_code == 204

    with db_module.session_factory() as s:
        u = s.get(User, admin_user["id"])
        assert verify_password("new-admin-pass-1234", u.password_hash)
        assert not verify_password(admin_user["password"], u.password_hash)


def test_change_password_wrong_old_returns_401(authed_app_client):
    resp = authed_app_client.post(
        "/api/auth/change-password",
        json={"old_password": "WRONG", "new_password": "new-admin-pass-1234"},
    )
    assert resp.status_code == 401


def test_change_password_short_new_returns_422(authed_app_client, admin_user):
    resp = authed_app_client.post(
        "/api/auth/change-password",
        json={"old_password": admin_user["password"], "new_password": "short"},
    )
    assert resp.status_code == 422


def test_change_password_unauthenticated_returns_401(app_client):
    resp = app_client.post(
        "/api/auth/change-password",
        json={"old_password": "x", "new_password": "new-admin-pass-1234"},
    )
    assert resp.status_code == 401
