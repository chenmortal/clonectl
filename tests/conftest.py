import os

# Ensure JWT_SECRET is non-empty BEFORE any test code runs. pydantic-settings
# priority is: init kwargs > os.environ > .env file, so this overrides the
# empty `JWT_SECRET=` that lives in the dev-machine .env.
os.environ.setdefault("JWT_SECRET", "test-jwt-secret-32-bytes-padding")

import httpx
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

import app.db as db_module
from app.auth.security import hash_password
from app.auth.tokens import create_access_token
from app.config import Settings
from app.main import create_app
from app.models import User, UserRole


@pytest.fixture(autouse=True)
def _reset_settings_cache():
    """Each test sees a fresh Settings() — required because ``get_settings``
    is ``lru_cache``-d and otherwise the first call leaks the dev-machine's
    .env (which may have ``JWT_SECRET=`` empty) into subsequent tokens."""
    from app.config import get_settings

    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.fixture()
def db_engine():
    engine = create_engine(
        "sqlite://",
        poolclass=StaticPool,
        connect_args={"check_same_thread": False},
    )
    db_module.engine = engine
    db_module.session_factory = sessionmaker(bind=engine, expire_on_commit=False)
    db_module.Base.metadata.create_all(engine)
    yield engine
    engine.dispose()


@pytest.fixture()
def session(db_engine):
    with db_module.session_factory() as session:
        yield session


class FakeRcloneClient:
    def __init__(self):
        self.remotes = []
        self.deleted = []
        self.started = []
        self.checks = []
        self.check_jobid = 77
        self.job_statuses = {}
        self.job_stats_map = {}
        self.start_exception = None
        self.ping_ok = True
        # verification probes (used by /api/data-sources/{id}/verify)
        self.list_calls: list[str] = []
        self.write_probe_calls: list[str] = []
        self.list_fail: Exception | None = None
        self.write_fail: Exception | None = None
        self.write_cleanup_fail: Exception | None = None

    def ping(self):
        return self.ping_ok

    def create_remote(self, name, type_, parameters):
        self.remotes.append({"name": name, "type": type_, "parameters": parameters})
        return {}

    def delete_remote(self, name):
        self.deleted.append(name)
        return {}

    def start_sync(self, src_fs, dst_fs, mode, options=None):
        if self.start_exception is not None:
            raise self.start_exception
        self.started.append(
            {"srcFs": src_fs, "dstFs": dst_fs, "mode": mode, "options": options}
        )
        return 42

    def start_check(self, src_fs, dst_fs, options=None):
        if self.start_exception is not None:
            raise self.start_exception
        self.checks.append({"srcFs": src_fs, "dstFs": dst_fs, "options": options})
        return self.check_jobid

    def job_status(self, job_id):
        return self.job_statuses.get(job_id, {"finished": True, "success": True, "error": ""})

    def job_stats(self, job_id):
        return self.job_stats_map.get(
            job_id, {"bytes": 100, "transfers": 2, "errors": 0, "elapsedTime": 1.5}
        )

    def list(self, remote: str) -> list[dict]:
        self.list_calls.append(remote)
        if self.list_fail is not None:
            raise self.list_fail
        return []

    def write_probe(self, remote: str) -> tuple[bool, str | None]:
        self.write_probe_calls.append(remote)
        if self.write_fail is not None:
            return False, str(self.write_fail)
        if self.write_cleanup_fail is not None:
            return True, f"delete failed: {self.write_cleanup_fail}"
        return True, None

    def close(self):
        pass


@pytest.fixture()
def fake_client():
    return FakeRcloneClient()


@pytest.fixture()
def proxy_requests():
    return []


@pytest.fixture()
def app_client(db_engine, fake_client, proxy_requests):
    settings = Settings(
        _env_file=None,
        database_url="sqlite://",
        poll_interval_seconds=3600,
        rclone_managed=False,
    )
    application = create_app(settings)

    def proxy_handler(request):
        proxy_requests.append(request)
        return httpx.Response(
            200, json={"proxied": True, "path": request.url.path}, headers={"x-rclone": "rcd"}
        )

    with TestClient(application) as client:
        application.state.rclone_client = fake_client
        application.state.proxy_client = httpx.Client(
            base_url="http://rc.test",
            auth=("admin", "pass"),
            transport=httpx.MockTransport(proxy_handler),
        )
        yield client


@pytest.fixture()
def authed_app_client(admin_user, db_engine, fake_client, proxy_requests):
    """Same as ``app_client`` but pre-authenticated as an admin via bearer token.

    Depends on ``admin_user`` so the underlying user row is shared with tests
    that also need the (username, password) pair.
    """
    token = create_access_token(
        user_id=admin_user["id"], username=admin_user["username"], role="admin"
    )
    headers = {"Authorization": f"Bearer {token}"}

    settings = Settings(
        _env_file=None,
        database_url="sqlite://",
        poll_interval_seconds=3600,
        rclone_managed=False,
    )
    application = create_app(settings)

    def proxy_handler(request):
        proxy_requests.append(request)
        return httpx.Response(
            200, json={"proxied": True, "path": request.url.path}, headers={"x-rclone": "rcd"}
        )

    with TestClient(application, headers=headers) as client:
        application.state.rclone_client = fake_client
        application.state.proxy_client = httpx.Client(
            base_url="http://rc.test",
            auth=("admin", "pass"),
            transport=httpx.MockTransport(proxy_handler),
        )
        yield client


@pytest.fixture()
def admin_user(db_engine):
    """Insert an admin user and return (username, password)."""
    with db_module.session_factory() as session:
        user = User(
            username="admin-test",
            password_hash=hash_password("admin-pass-1234"),
            role=UserRole.admin,
        )
        session.add(user)
        session.commit()
        session.refresh(user)
        return {"id": user.id, "username": user.username, "password": "admin-pass-1234"}


@pytest.fixture()
def admin_token(app_client, admin_user):
    """Log in via /api/auth/login and return the bearer token."""
    resp = app_client.post(
        "/api/auth/login",
        json={"username": admin_user["username"], "password": admin_user["password"]},
    )
    assert resp.status_code == 200, resp.text
    return resp.json()["access_token"]


@pytest.fixture()
def admin_headers(admin_token):
    return {"Authorization": f"Bearer {admin_token}"}
