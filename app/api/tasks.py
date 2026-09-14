from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import select
from sqlalchemy.orm import Session

from app import models, schemas
from app.api.deps import require_leader
from app.auth.deps import require_role
from app.db import get_session
from app.models import RunTrigger, UserRole
from app.services import task as task_service
from app.services.runner import run_task

router = APIRouter(
    prefix="/api/tasks",
    tags=["tasks"],
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
    """Validate that the chosen source/destination FKs exist.

    A task uses EITHER the legacy (storage_id) pair OR the new (data_source_id)
    pair — never both. The Pydantic schema enforces the invariant; this
    function just checks the rows exist.
    """
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
    # Defensive: should be unreachable because the Pydantic validator rejects
    # both-null and both-set. Surfacing 400 with a clear message anyway.
    raise HTTPException(
        status_code=400,
        detail="must set src_data_source_id/dst_data_source_id or "
               "src_storage_id/dst_storage_id",
    )


def _validate_pre_check(session: Session, pre_check_task_id: int | None) -> None:
    if pre_check_task_id is not None and session.get(models.CheckTask, pre_check_task_id) is None:
        raise HTTPException(
            status_code=400, detail=f"check task {pre_check_task_id} not found"
        )


def _get_task_or_404(session: Session, task_id: int) -> models.SyncTask:
    task = session.get(models.SyncTask, task_id)
    if task is None:
        raise HTTPException(status_code=404, detail="task not found")
    return task


def _apply_schedule(request: Request, task: models.SyncTask) -> None:
    scheduler = getattr(request.app.state, "scheduler", None)
    if scheduler is None:
        return
    if task.enabled:
        scheduler.register_task(task)
    else:
        scheduler.remove_task(task.id)


@router.get("", response_model=list[schemas.TaskOut])
def list_tasks(session: SessionDep):
    return session.scalars(select(models.SyncTask)).all()


@router.post(
    "", response_model=schemas.TaskOut, status_code=201,
    dependencies=_WRITE_ROLE_DEPS,
)
def create_task(data: schemas.TaskCreate, request: Request, session: SessionDep):
    _validate_refs(
        session,
        data.src_storage_id, data.dst_storage_id,
        data.src_data_source_id, data.dst_data_source_id,
    )
    _validate_pre_check(session, data.pre_check_task_id)
    task = task_service.create_task(session, data)
    _apply_schedule(request, task)
    return task


@router.get("/{task_id}", response_model=schemas.TaskOut)
def get_task(task_id: int, session: SessionDep):
    return _get_task_or_404(session, task_id)


@router.put(
    "/{task_id}", response_model=schemas.TaskOut,
    dependencies=_WRITE_ROLE_DEPS,
)
def update_task(task_id: int, data: schemas.TaskUpdate, request: Request, session: SessionDep):
    task = _get_task_or_404(session, task_id)
    update = data.model_dump(exclude_unset=True)
    src_id = update.get("src_storage_id", task.src_storage_id)
    dst_id = update.get("dst_storage_id", task.dst_storage_id)
    src_ds_id = update.get("src_data_source_id", task.src_data_source_id)
    dst_ds_id = update.get("dst_data_source_id", task.dst_data_source_id)
    _validate_refs(session, src_id, dst_id, src_ds_id, dst_ds_id)
    if "pre_check_task_id" in update:
        _validate_pre_check(session, update["pre_check_task_id"])
    task = task_service.update_task(session, task, data)
    _apply_schedule(request, task)
    return task


@router.delete(
    "/{task_id}", status_code=204,
    dependencies=_WRITE_ROLE_DEPS,
)
def delete_task(task_id: int, request: Request, session: SessionDep):
    task = _get_task_or_404(session, task_id)
    scheduler = getattr(request.app.state, "scheduler", None)
    if scheduler is not None:
        scheduler.remove_task(task.id)
    session.delete(task)
    session.commit()


@router.post(
    "/{task_id}/trigger", response_model=schemas.RunOut, status_code=202,
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit))],
)
def trigger_task(task_id: int, request: Request, session: SessionDep):
    _get_task_or_404(session, task_id)
    client = request.app.state.rclone_client
    run = run_task(session, client, task_id, RunTrigger.manual)
    return run
