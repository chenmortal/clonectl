from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import select
from sqlalchemy.orm import Session

from app import models, schemas
from app.auth.deps import require_role
from app.db import get_session
from app.models import RunStatus, UserRole
from app.rclone.client import RcloneApiError

router = APIRouter(
    prefix="/api/checks",
    tags=["checks"],
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)

SessionDep = Annotated[Session, Depends(get_session)]


@router.get("", response_model=list[schemas.CheckOut])
def list_checks(
    session: SessionDep,
    task_id: int | None = None,
    status: RunStatus | None = None,
    limit: int = 100,
):
    stmt = select(models.CheckRun).order_by(models.CheckRun.id.desc()).limit(limit)
    if task_id is not None:
        stmt = stmt.where(models.CheckRun.task_id == task_id)
    if status is not None:
        stmt = stmt.where(models.CheckRun.status == status)
    return session.scalars(stmt).all()


@router.get("/{check_id}", response_model=schemas.CheckDetail)
def get_check(check_id: int, request: Request, session: SessionDep):
    check = session.get(models.CheckRun, check_id)
    if check is None:
        raise HTTPException(status_code=404, detail="check not found")
    detail = schemas.CheckDetail.model_validate(check)
    if check.status == RunStatus.running and check.job_id is not None:
        client = request.app.state.rclone_client
        try:
            detail.live = {"status": client.job_status(check.job_id)}
        except RcloneApiError as exc:
            detail.live = {"error": str(exc)}
    return detail
