"""Tests for active-standby leader election and API gating.

These tests run on SQLite (single-writer) which is sufficient to verify the
CAS-style UPDATE and the API gating logic. The real concurrent behavior on
MySQL is exercised by the production deployment, not the unit tests.
"""
from __future__ import annotations

from datetime import datetime, timedelta

import pytest
from fastapi.testclient import TestClient

import app.db as db_module
from app.api.deps import require_leader
from app.cluster.election import LeaderElector
from app.config import Settings
from app.main import create_app


def _make_elector(
    session_factory,
    *,
    node_id: str,
    on_acquired=None,
    on_lost=None,
    lease_duration: int = 10,
    heartbeat_interval: int = 3,
) -> LeaderElector:
    return LeaderElector(
        session_factory,
        node_id=node_id,
        cluster_name="test-cluster",
        heartbeat_interval_seconds=heartbeat_interval,
        lease_duration_seconds=lease_duration,
        on_acquired=on_acquired,
        on_lost=on_lost,
    )


def _seed_lease(session_factory) -> None:
    """Insert a leader_lease row directly without going through start()."""
    from app.models import LeaderLease

    with session_factory() as session:
        if session.get(LeaderLease, "test-cluster") is None:
            session.add(LeaderLease(cluster_name="test-cluster", epoch=0))
            session.commit()


# ---------------------------------------------------------------------------
# LeaderElector unit tests
# ---------------------------------------------------------------------------


def test_acquire_when_lease_unowned(db_engine, session):
    """First election on a fresh cluster should grant leadership."""
    _seed_lease(db_module.session_factory)
    elector = _make_elector(db_module.session_factory, node_id="nodeA")
    elector.start()  # registers node, then runs one elect tick

    # Manually drive an elect cycle to avoid timing flakiness
    elector._elect_tick()  # noqa: SLF001 — intentional in tests

    assert elector.is_leader is True
    assert elector.leader_id == "nodeA"
    elector.stop()


def test_acquire_blocked_by_valid_lease_held_by_another(db_engine, session):
    """A peer with a non-expired lease cannot be pre-empted."""
    from app.models import LeaderLease

    _seed_lease(db_module.session_factory)
    # Peer "nodeB" already holds a fresh lease — update the seeded row
    with db_module.session_factory() as s:
        lease = s.get(LeaderLease, "test-cluster")
        lease.leader_node_id = "nodeB"
        lease.lease_until = datetime.utcnow() + timedelta(seconds=30)
        lease.epoch = 1
        s.commit()

    elector = _make_elector(db_module.session_factory, node_id="nodeA")
    elector.start()
    elector._elect_tick()  # noqa: SLF001

    assert elector.is_leader is False
    assert elector.leader_id == "nodeB"
    elector.stop()


def test_acquire_when_other_lease_expired(db_engine, session):
    """An expired lease held by another node can be taken over."""
    from app.models import LeaderLease

    _seed_lease(db_module.session_factory)
    with db_module.session_factory() as s:
        lease = s.get(LeaderLease, "test-cluster")
        lease.leader_node_id = "nodeOld"
        lease.lease_until = datetime.utcnow() - timedelta(seconds=1)
        lease.epoch = 5
        s.commit()

    elector = _make_elector(db_module.session_factory, node_id="nodeA")
    elector.start()
    elector._elect_tick()  # noqa: SLF001

    assert elector.is_leader is True
    assert elector.leader_id == "nodeA"
    elector.stop()


def test_callbacks_fire_on_acquire_and_lose(db_engine, session):
    acquired: list[int] = []
    lost: list[int] = []

    _seed_lease(db_module.session_factory)
    elector = _make_elector(
        db_module.session_factory,
        node_id="nodeA",
        on_acquired=lambda: acquired.append(1),
        on_lost=lambda: lost.append(1),
    )
    elector.start()
    elector._elect_tick()
    assert acquired == [1]
    assert lost == []

    # Simulate losing the lease by clearing leadership flag
    elector._set_leader_state(False)
    assert lost == [1]
    elector.stop()


def test_snapshot_shape(db_engine, session):
    _seed_lease(db_module.session_factory)
    elector = _make_elector(db_module.session_factory, node_id="nodeA")
    elector.start()
    snap = elector.snapshot()
    assert set(snap.keys()) == {"is_leader", "node_id", "cluster_name", "leader_id", "peers"}
    assert snap["node_id"] == "nodeA"
    assert snap["cluster_name"] == "test-cluster"
    assert snap["peers"] == []  # only us so far
    elector.stop()


def test_stop_releases_lease(db_engine, session):
    from app.models import LeaderLease

    _seed_lease(db_module.session_factory)
    elector = _make_elector(db_module.session_factory, node_id="nodeA")
    elector.start()
    elector._elect_tick()
    assert elector.is_leader is True

    elector.stop()
    with db_module.session_factory() as s:
        lease = s.get(LeaderLease, "test-cluster")
        assert lease is not None
        assert lease.leader_node_id is None
        assert lease.lease_until is None


def test_heartbeat_extends_lease_when_leader(db_engine, session):
    from app.models import LeaderLease

    _seed_lease(db_module.session_factory)
    elector = _make_elector(
        db_module.session_factory, node_id="nodeA", lease_duration=10
    )
    elector.start()
    elector._elect_tick()
    with db_module.session_factory() as s:
        lease = s.get(LeaderLease, "test-cluster")
        first_lease = lease.lease_until

    elector._heartbeat_tick()
    with db_module.session_factory() as s:
        lease = s.get(LeaderLease, "test-cluster")
        second_lease = lease.lease_until

    assert second_lease > first_lease
    elector.stop()


# ---------------------------------------------------------------------------
# /healthz endpoint exposes cluster fields
# ---------------------------------------------------------------------------


def test_healthz_includes_cluster_fields_when_elector_present(db_engine, fake_client):
    settings = Settings(
        _env_file=None,
        database_url="sqlite://",
        poll_interval_seconds=3600,
        rclone_managed=False,
        cluster_name="test-cluster",
        node_id="nodeA",
    )
    application = create_app(settings)

    elector = LeaderElector(
        db_module.session_factory,
        node_id="nodeA",
        cluster_name="test-cluster",
        heartbeat_interval_seconds=3,
        lease_duration_seconds=10,
    )
    with TestClient(application) as client:
        application.state.rclone_client = fake_client
        application.state.elector = elector

        body = client.get("/healthz").json()
        assert body["node_id"] == "nodeA"
        assert body["cluster_name"] == "test-cluster"
        assert body["is_leader"] is False  # haven't elected yet
        assert "leader_id" in body
        assert "peers" in body


# ---------------------------------------------------------------------------
# API gating: writes on standby return 503
# ---------------------------------------------------------------------------


@pytest.fixture()
def standby_app(db_engine, fake_client, admin_user):
    """App with a LeaderElector wired in but force-elected as standby."""
    settings = Settings(
        _env_file=None,
        database_url="sqlite://",
        poll_interval_seconds=3600,
        rclone_managed=False,
        cluster_name="test-cluster",
        node_id="nodeStandby",
    )
    application = create_app(settings)

    # Pre-create a lease held by someone else so this elector stays standby
    from app.models import LeaderLease

    with db_module.session_factory() as s:
        if s.get(LeaderLease, "test-cluster") is None:
            s.add(LeaderLease(cluster_name="test-cluster", epoch=0))
        lease = s.get(LeaderLease, "test-cluster")
        lease.leader_node_id = "nodeReal"
        lease.lease_until = datetime.utcnow() + timedelta(seconds=30)
        s.commit()

    elector = LeaderElector(
        db_module.session_factory,
        node_id="nodeStandby",
        cluster_name="test-cluster",
        heartbeat_interval_seconds=3,
        lease_duration_seconds=10,
    )
    with TestClient(application) as client:
        application.state.rclone_client = fake_client
        application.state.elector = elector
        elector.start()  # populates leader_id via initial _elect_tick
        # log the test admin in via the same client so writes have a token
        login = client.post(
            "/api/auth/login",
            json={"username": admin_user["username"], "password": admin_user["password"]},
        )
        assert login.status_code == 200, login.text
        token = login.json()["access_token"]
        yield client, elector, {"Authorization": f"Bearer {token}"}
        elector.stop()


def test_writes_blocked_on_standby_with_503(standby_app):
    client, elector, headers = standby_app
    # No elect_tick was called on this elector → is_leader=False
    assert elector.is_leader is False

    resp = client.post(
        "/api/tasks",
        json={
            "name": "should-fail",
            "src_storage_id": 1,
            "src_path": "/a",
            "dst_storage_id": 1,
            "dst_path": "/b",
            "mode": "sync",
            "cron": "*/5 * * * *",
        },
        headers=headers,
    )
    assert resp.status_code == 503
    assert resp.headers.get("retry-after") == "5"
    body = resp.json()["detail"]
    assert body["error"] == "not_leader"
    assert body["leader_id"] == "nodeReal"
    assert body["cluster_name"] == "test-cluster"


def test_check_task_writes_blocked_on_standby(standby_app):
    client, elector, headers = standby_app
    resp = client.post(
        "/api/check-tasks",
        json={
            "name": "should-fail",
            "src_storage_id": 1,
            "src_path": "/a",
            "dst_storage_id": 1,
            "dst_path": "/b",
        },
        headers=headers,
    )
    assert resp.status_code == 503
    assert resp.json()["detail"]["error"] == "not_leader"


def test_reads_work_on_standby(standby_app):
    """Reads must not require leadership — standby serves a usable dashboard."""
    client, _, headers = standby_app
    resp = client.get("/api/tasks", headers=headers)
    assert resp.status_code == 200
    assert resp.json() == []


# ---------------------------------------------------------------------------
# require_leader dependency unit tests
# ---------------------------------------------------------------------------


def test_require_leader_pass_when_elector_none():
    """No elector configured → behaves as single-node, pass through."""
    from fastapi import Request

    req = Request(scope={"type": "http", "app": _StubApp(state=None)})
    assert require_leader(req) is None


class _StubApp:
    def __init__(self, state):
        self.state = state
