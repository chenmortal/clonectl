"""System settings: singleton key/value runtime configuration. Admin only."""
from __future__ import annotations

from typing import Annotated

import httpx
from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import select
from sqlalchemy.orm import Session

from app import models, schemas
from app.auth.deps import CurrentUser, require_role
from app.db import get_session
from app.models import SystemSetting, UserRole
from app.rclone.client import RcloneApiError
from app.services.alerts import build_alertmanager_payload, post_to_alertmanager

router = APIRouter(
    prefix="/api/system-settings",
    tags=["system-settings"],
    dependencies=[Depends(require_role(UserRole.admin))],
)

SessionDep = Annotated[Session, Depends(get_session)]


def _validate_url(key: str, value: str) -> None:
    """Special validation for ``alertmanager_url``: must be empty or a valid http(s) URL."""
    if key != "alertmanager_url" or not value:
        return
    try:
        u = httpx.URL(value)
    except Exception as exc:
        raise HTTPException(
            status_code=422,
            detail=f"alertmanager_url is not a valid URL: {exc}",
        ) from exc
    if u.scheme not in ("http", "https"):
        raise HTTPException(
            status_code=422,
            detail=f"alertmanager_url scheme must be http or https, got {u.scheme!r}",
        )


@router.get("", response_model=list[schemas.SystemSettingOut])
def list_settings(session: SessionDep):
    return session.scalars(
        select(SystemSetting).order_by(SystemSetting.key)
    ).all()


@router.get("/{key}", response_model=schemas.SystemSettingOut)
def get_setting(key: str, session: SessionDep):
    row = session.get(SystemSetting, key)
    if row is None:
        raise HTTPException(status_code=404, detail="setting not found")
    return row


@router.put("/{key}", response_model=schemas.SystemSettingOut)
def upsert_setting(
    key: str,
    data: schemas.SystemSettingUpdate,
    session: SessionDep,
    current_user: CurrentUser,
):
    _validate_url(key, data.value)
    row = session.get(SystemSetting, key)
    if row is None:
        row = SystemSetting(
            key=key, value=data.value, updated_by_user_id=current_user.id,
        )
        session.add(row)
    else:
        row.value = data.value
        row.updated_by_user_id = current_user.id
    session.commit()
    session.refresh(row)
    return row


@router.post(
    "/internal/alertmanager-test",
    response_model=schemas.AlertmanagerTestOut,
)
def alertmanager_test(
    data: schemas.AlertmanagerTestIn,
    session: SessionDep,
):
    """Admin-only test endpoint: send a sample v4 webhook to the configured URL."""
    url = session.get(SystemSetting, "alertmanager_url")
    if url is None or not url.value:
        raise HTTPException(
            status_code=400,
            detail="alertmanager_url not configured; set it first via PUT /api/system-settings/alertmanager_url",
        )
    payload = build_alertmanager_payload(
        task_id=0, task_name=data.alertname, run_id=0,
        error_text="manual test alert", started_at=None,
        status="firing", labels={"alertname": data.alertname, **data.labels},
        is_sync=True,
    )
    sent, status_code, error = post_to_alertmanager(url.value, payload)
    return schemas.AlertmanagerTestOut(
        sent=sent, status_code=status_code, error=error,
    )
