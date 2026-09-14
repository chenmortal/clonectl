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
    prefix="/api/runs",
    tags=["runs"],
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)

SessionDep = Annotated[Session, Depends(get_session)]


@router.get("", response_model=list[schemas.RunOut])
def list_runs(
    session: SessionDep,
    task_id: int | None = None,
    status: RunStatus | None = None,
    limit: int = 100,
):
    stmt = select(models.SyncRun).order_by(models.SyncRun.id.desc()).limit(limit)
    if task_id is not None:
        stmt = stmt.where(models.SyncRun.task_id == task_id)
    if status is not None:
        stmt = stmt.where(models.SyncRun.status == status)
    return session.scalars(stmt).all()


@router.get("/{run_id}", response_model=schemas.RunDetail)
def get_run(run_id: int, request: Request, session: SessionDep):
    run = session.get(models.SyncRun, run_id)
    if run is None:
        raise HTTPException(status_code=404, detail="run not found")
    detail = schemas.RunDetail.model_validate(run)
    if run.status == RunStatus.running and run.job_id is not None:
        client = request.app.state.rclone_client
        try:
            job_status = client.job_status(run.job_id)
            stats = client.job_stats(run.job_id)
            # `/core/stats` (returned by job_stats) carries bytes / transferring /
            # speed but NOT totalBytes — that field lives under
            # `/job/status`["stats"]. Stitch them so the frontend can render
            # ``bytes / totalBytes`` and a meaningful percentage. Without this
            # merge, the monitor panel falls back to 0% and the "已传输"
            # cell shows ``... / -`` because totalBytes is undefined.
            job_stats = (job_status or {}).get("stats") or {}
            if "totalBytes" in job_stats and "totalBytes" not in stats:
                stats["totalBytes"] = job_stats["totalBytes"]
            detail.live = {"status": job_status, "stats": stats}
        except RcloneApiError as exc:
            detail.live = {"error": str(exc)}
    return detail
