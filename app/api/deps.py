"""Shared FastAPI dependencies for API routes."""
from __future__ import annotations

from fastapi import HTTPException, Request


def require_leader(request: Request):
    """Reject write requests when this node is not the active leader.

    Returns the elector on success (leader) so callers can introspect if needed.
    When no elector is configured (single-node / tests), the dependency is a
    no-op pass-through — HA is opt-in.
    """
    elector = getattr(request.app.state, "elector", None)
    if elector is None or elector.is_leader:
        return elector
    raise HTTPException(
        status_code=503,
        detail={
            "error": "not_leader",
            "leader_id": elector.leader_id,
            "cluster_name": elector.cluster_name,
            "retry_after": 5,
        },
        headers={"Retry-After": "5"},
    )
