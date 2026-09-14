from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import select
from sqlalchemy.orm import Session

from app import models, schemas
from app.api.deps import require_leader
from app.auth.deps import require_role
from app.db import get_session
from app.models import RunTrigger, UserRole
from app.services.check import run_check

router = APIRouter(
    prefix="/api/check-tasks",
    tags=["check-tasks"],
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)

SessionDep = Annotated[Session, Depends(get_session)]

_WRITE_ROLE_DEPS = [
    Depends(require_leader),
    Depends(require_role(UserRole.admin, UserRole.edit)),
]


def _validate_refs(
    session: Session,
    src_storage_id: int | None,
    dst_storage_id: int | None,
    src_data_source_id: int | None,
    dst_data_source_id: int | None,
) -> None:
    """See ``app/api/tasks.py:_validate_refs`` — same contract for check tasks."""
    if src_storage_id is not None and dst_storage_id is not None:
        for storage_id in (src_storage_id, dst_storage_id):
            if session.get(models.StorageConfig, storage_id) is None:
                raise HTTPException(
                    status_code=400, detail=f"storage {storage_id} not found"
                )
        return
    if src_data_source_id is not None and dst_data_source_id is not None:
        for ds_id in (src_data_source_id, dst_data_source_id):
            if session.get(models.DataSource, ds_id) is None:
                raise HTTPException(
                    status_code=400, detail=f"data source {ds_id} not found"
                )
        return
    raise HTTPException(
        status_code=400,
        detail="must set src_data_source_id/dst_data_source_id or "
               "src_storage_id/dst_storage_id",
    )


def _get_or_404(session: Session, check_task_id: int) -> models.CheckTask:
    check_task = session.get(models.CheckTask, check_task_id)
    if check_task is None:
        raise HTTPException(status_code=404, detail="check task not found")
    return check_task


def _apply_schedule(request: Request, check_task: models.CheckTask) -> None:
    scheduler = getattr(request.app.state, "scheduler", None)
    if scheduler is None:
        return
    if check_task.enabled:
        scheduler.register_check_task(check_task)
    else:
        scheduler.remove_check_task(check_task.id)


@router.get("", response_model=list[schemas.CheckTaskOut])
def list_check_tasks(session: SessionDep):
    return session.scalars(select(models.CheckTask)).all()


@router.post(
    "", response_model=schemas.CheckTaskOut, status_code=201,
    dependencies=_WRITE_ROLE_DEPS,
)
def create_check_task(
    data: schemas.CheckTaskCreate, request: Request, session: SessionDep
):
    _validate_refs(
        session,
        data.src_storage_id, data.dst_storage_id,
        data.src_data_source_id, data.dst_data_source_id,
    )
    check_task = models.CheckTask(**data.model_dump())
    session.add(check_task)
    session.commit()
    session.refresh(check_task)
    _apply_schedule(request, check_task)
    return check_task


@router.get("/{check_task_id}", response_model=schemas.CheckTaskOut)
def get_check_task(check_task_id: int, session: SessionDep):
    return _get_or_404(session, check_task_id)


@router.put(
    "/{check_task_id}", response_model=schemas.CheckTaskOut,
    dependencies=_WRITE_ROLE_DEPS,
)
def update_check_task(
    check_task_id: int, data: schemas.CheckTaskUpdate, request: Request, session: SessionDep
):
    check_task = _get_or_404(session, check_task_id)
    update = data.model_dump(exclude_unset=True)
    src_id = update.get("src_storage_id", check_task.src_storage_id)
    dst_id = update.get("dst_storage_id", check_task.dst_storage_id)
    src_ds_id = update.get("src_data_source_id", check_task.src_data_source_id)
    dst_ds_id = update.get("dst_data_source_id", check_task.dst_data_source_id)
    _validate_refs(session, src_id, dst_id, src_ds_id, dst_ds_id)
    for key, value in update.items():
        setattr(check_task, key, value)
    session.commit()
    session.refresh(check_task)
    _apply_schedule(request, check_task)
    return check_task


@router.delete(
    "/{check_task_id}", status_code=204,
    dependencies=_WRITE_ROLE_DEPS,
)
def delete_check_task(check_task_id: int, request: Request, session: SessionDep):
    check_task = _get_or_404(session, check_task_id)
    used = session.scalar(
        select(models.SyncTask).where(models.SyncTask.pre_check_task_id == check_task_id)
    )
    if used is not None:
        raise HTTPException(
            status_code=409, detail="check task is used as pre-check of a sync task"
        )
    scheduler = getattr(request.app.state, "scheduler", None)
    if scheduler is not None:
        scheduler.remove_check_task(check_task.id)
    session.delete(check_task)
    session.commit()


@router.post(
    "/{check_task_id}/trigger", response_model=schemas.CheckOut, status_code=202,
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit))],
)
def trigger_check_task(check_task_id: int, request: Request, session: SessionDep):
    _get_or_404(session, check_task_id)
    client = request.app.state.rclone_client
    check = run_check(session, client, check_task_id, RunTrigger.manual)
    return check
