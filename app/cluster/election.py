"""Active-standby leader election via DB row + lease renewal.

Two nodes share one MySQL database. Each node runs two periodic jobs:

* ``_heartbeat_tick`` (every ``heartbeat_interval_seconds``) refreshes the
  node's own ``cluster_nodes.last_heartbeat``; if the node is currently the
  leader, it also extends ``leader_lease.lease_until`` to
  ``NOW() + lease_duration_seconds``. If that extension finds the lease is no
  longer owned by us (``rowcount == 0``), we drop leadership defensively.

* ``_elect_tick`` (every 1 s) reads the current ``leader_lease`` row. If the
  lease is unowned or expired, it attempts a CAS UPDATE that only one
  concurrent caller can win (``rowcount == 1``). On win, fires ``on_acquired``;
  on loss of a previously held lease, fires ``on_lost``.

Concurrency safety comes from MySQL InnoDB row locks on the single
``leader_lease`` row plus the WHERE-clause re-evaluation inside the UPDATE;
no ``SELECT FOR UPDATE`` is needed. The same logic works against SQLite
for tests (single-writer semantics).
"""
from __future__ import annotations

import logging
import os
import socket
from collections.abc import Callable
from datetime import datetime, timedelta
from typing import Any

from apscheduler.schedulers.background import BackgroundScheduler
from sqlalchemy import text
from sqlalchemy.orm import sessionmaker

from app.models import ClusterNode, LeaderLease

logger = logging.getLogger(__name__)

HEARTBEAT_JOB_ID = "cluster-heartbeat"
ELECT_JOB_ID = "cluster-elect"
ELECT_INTERVAL_SECONDS = 30


class LeaderElector:
    """Per-process leader-election participant."""

    def __init__(
        self,
        session_factory: sessionmaker,
        *,
        node_id: str = "",
        cluster_name: str = "default",
        heartbeat_interval_seconds: int = 3,
        lease_duration_seconds: int = 10,
        on_acquired: Callable[[], None] | None = None,
        on_lost: Callable[[], None] | None = None,
    ):
        self.session_factory = session_factory
        self.node_id = node_id or f"{socket.gethostname()}-{os.getpid()}"
        self.cluster_name = cluster_name
        self.heartbeat_interval = heartbeat_interval_seconds
        self.lease_duration = lease_duration_seconds
        self._on_acquired = on_acquired
        self._on_lost = on_lost

        self._scheduler = BackgroundScheduler()
        self._is_leader = False
        self._leader_id: str | None = None

    # ---- public surface ----

    @property
    def is_leader(self) -> bool:
        return self._is_leader

    @property
    def leader_id(self) -> str | None:
        return self._leader_id

    def snapshot(self) -> dict[str, Any]:
        """Read-only view for ``/healthz`` and CLI ``status``."""
        return {
            "is_leader": self._is_leader,
            "node_id": self.node_id,
            "cluster_name": self.cluster_name,
            "leader_id": self._leader_id,
            "peers": [p for p in self._list_active_peers() if p != self.node_id],
        }

    def start(self) -> None:
        """Register this node and start heartbeat + elect loops."""
        hostname = socket.gethostname()
        now = datetime.utcnow()
        with self.session_factory() as session:
            existing_lease = session.get(LeaderLease, self.cluster_name)
            if existing_lease is None:
                session.add(
                    LeaderLease(
                        cluster_name=self.cluster_name,
                        leader_node_id=None,
                        lease_until=None,
                        epoch=0,
                    )
                )
            # Idempotent (re-)join: delete any stale row for this node_id, then insert.
            session.execute(
                text("DELETE FROM cluster_nodes WHERE node_id = :nid"),
                {"nid": self.node_id},
            )
            session.add(
                ClusterNode(
                    node_id=self.node_id,
                    cluster_name=self.cluster_name,
                    role="standby",
                    hostname=hostname,
                    ip_address=None,
                    started_at=now,
                    last_heartbeat=now,
                    left_at=None,
                )
            )
            session.commit()

        self._scheduler.add_job(
            self._heartbeat_tick,
            trigger="interval",
            seconds=self.heartbeat_interval,
            id=HEARTBEAT_JOB_ID,
            replace_existing=True,
            max_instances=1,
            coalesce=True,
        )
        self._scheduler.add_job(
            self._elect_tick,
            trigger="interval",
            seconds=ELECT_INTERVAL_SECONDS,
            id=ELECT_JOB_ID,
            replace_existing=True,
            max_instances=1,
            coalesce=True,
        )
        self._scheduler.start()
        # Run one election cycle synchronously so callers immediately know
        # whether we hold leadership and who does (for /healthz + 503 detail).
        try:
            self._elect_tick()
        except Exception:
            logger.exception("initial elect_tick failed; will retry on next interval")
        logger.info(
            "LeaderElector started: node_id=%s cluster=%s",
            self.node_id,
            self.cluster_name,
        )

    def stop(self) -> None:
        """Mark node left; release lease if we held it."""
        if self._scheduler.running:
            self._scheduler.shutdown(wait=False)
        now = datetime.utcnow()
        with self.session_factory() as session:
            if self._is_leader:
                session.execute(
                    text(
                        "UPDATE leader_lease "
                        "SET leader_node_id = NULL, lease_until = NULL "
                        "WHERE cluster_name = :cname AND leader_node_id = :nid"
                    ),
                    {"cname": self.cluster_name, "nid": self.node_id},
                )
                self._is_leader = False
                self._leader_id = None
            session.execute(
                text(
                    "UPDATE cluster_nodes SET role = 'left', left_at = :now "
                    "WHERE node_id = :nid"
                ),
                {"now": now, "nid": self.node_id},
            )
            session.commit()
        logger.info("LeaderElector stopped: node_id=%s", self.node_id)

    # ---- internal jobs (also reused by tests) ----

    def _heartbeat_tick(self) -> None:
        """Update liveness; if leader, extend lease."""
        try:
            now = datetime.utcnow()
            with self.session_factory() as session:
                session.execute(
                    text(
                        "UPDATE cluster_nodes SET last_heartbeat = :now "
                        "WHERE node_id = :nid"
                    ),
                    {"now": now, "nid": self.node_id},
                )
                if self._is_leader:
                    lease_until = now + timedelta(seconds=self.lease_duration)
                    result = session.execute(
                        text(
                            "UPDATE leader_lease "
                            "SET lease_until = :lu "
                            "WHERE cluster_name = :cname AND leader_node_id = :nid"
                        ),
                        {
                            "lu": lease_until,
                            "cname": self.cluster_name,
                            "nid": self.node_id,
                        },
                    )
                    if result.rowcount == 0:
                        logger.warning(
                            "lease heartbeat failed (no longer owner); releasing leadership"
                        )
                        self._set_leader_state(False)
                session.commit()
        except Exception:
            logger.exception("LeaderElector heartbeat failed")

    def _elect_tick(self) -> None:
        """Try to claim leadership if the lease is unowned or expired."""
        try:
            with self.session_factory() as session:
                lease = session.get(LeaderLease, self.cluster_name)
                if lease is None:
                    # Row vanished (manual ops); recreate and let the next tick try.
                    session.add(
                        LeaderLease(cluster_name=self.cluster_name, epoch=0)
                    )
                    session.commit()
                    return

                now = datetime.utcnow()
                lease_expired = (
                    lease.leader_node_id is None
                    or lease.lease_until is None
                    or lease.lease_until <= now
                )

                if not lease_expired:
                    # someone else holds a valid lease
                    self._leader_id = lease.leader_node_id
                    if self._is_leader and lease.leader_node_id != self.node_id:
                        self._set_leader_state(False)
                    return

                lease_until = now + timedelta(seconds=self.lease_duration)
                result = session.execute(
                    text(
                        "UPDATE leader_lease "
                        "SET leader_node_id = :nid, "
                        "    lease_until = :lu, "
                        "    epoch = epoch + 1 "
                        "WHERE cluster_name = :cname "
                        "  AND (leader_node_id IS NULL OR lease_until <= :now)"
                    ),
                    {
                        "nid": self.node_id,
                        "lu": lease_until,
                        "cname": self.cluster_name,
                        "now": now,
                    },
                )
                if result.rowcount == 1:
                    session.execute(
                        text(
                            "UPDATE cluster_nodes SET role = 'leader' "
                            "WHERE node_id = :nid"
                        ),
                        {"nid": self.node_id},
                    )
                    session.commit()
                    logger.debug(
                        "acquired leadership node_id=%s epoch=%d",
                        self.node_id,
                        lease.epoch + 1,
                    )
                    self._set_leader_state(True)
                else:
                    # Lost the race; just commit nothing and try again next tick.
                    session.rollback()
        except Exception:
            logger.exception("LeaderElector elect tick failed")

    def _set_leader_state(self, value: bool) -> None:
        if value == self._is_leader:
            self._leader_id = self.node_id if value else self._leader_id
            return
        self._is_leader = value
        if value:
            self._leader_id = self.node_id
            if self._on_acquired:
                try:
                    self._on_acquired()
                except Exception:
                    logger.exception("on_acquired callback failed")
        else:
            if self._on_lost:
                try:
                    self._on_lost()
                except Exception:
                    logger.exception("on_lost callback failed")

    def _list_active_peers(self) -> list[str]:
        """node_ids considered live (heartbeat within lease window)."""
        try:
            cutoff = datetime.utcnow() - timedelta(
                seconds=max(2 * self.heartbeat_interval, self.lease_duration)
            )
            with self.session_factory() as session:
                rows = session.execute(
                    text(
                        "SELECT node_id FROM cluster_nodes "
                        "WHERE cluster_name = :cname "
                        "  AND left_at IS NULL "
                        "  AND last_heartbeat >= :cutoff"
                    ),
                    {"cname": self.cluster_name, "cutoff": cutoff},
                ).fetchall()
            return [r[0] for r in rows]
        except Exception:
            logger.exception("LeaderElector peer list failed")
            return []
