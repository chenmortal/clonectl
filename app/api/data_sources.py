"""DataSource API: end-user "bucket + path" bound to a StorageSource.

Includes CRUD, /bindings sub-routes for the M:N permission table, and
/verify for read+write probe.
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import or_, select
from sqlalchemy.orm import Session

from app import models, schemas
from app.api.deps import require_leader
from app.api.storage_sources import build_remote_parameters, _ds_remote_name
from app.auth.deps import (
    CurrentUser,
    check_datasource_access,
    filter_query_for_datasource_access,
    require_datasource_access,
    require_role,
)
from app.db import get_session
from app.models import (
    BindingPermission,
    DataSource,
    DataSourceBinding,
    User,
    UserRole,
)
from app.rclone.client import RcloneApiError

router = APIRouter(
    prefix="/api/data-sources",
    tags=["data-sources"],
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)

SessionDep = Annotated[Session, Depends(get_session)]


def _enforce_credentials_for_remote_backends(
    storage_source: models.StorageSource,
    access_key_id: str | None,
    secret_access_key: str | None,
) -> None:
    """Cross-field rule for data sources: AK/SK are required unless the
    linked StorageSource.type is ``"local"`` (rclone's local backend
    ignores credentials). Raises 400 with a clear message on violation.
    """
    if storage_source.type == "local":
        return
    if not (access_key_id and secret_access_key):
        raise HTTPException(
            status_code=400,
            detail=(
                f"storage source type {storage_source.type!r} requires "
                "access_key_id and secret_access_key"
            ),
        )


# --- CRUD -----------------------------------------------------------------


@router.get("", response_model=list[schemas.DataSourceOut])
def list_data_sources(current_user: CurrentUser, session: SessionDep):
    """Admin sees all; non-admin sees owned + bound rows."""
    qs = select(DataSource).order_by(DataSource.id)
    qs = filter_query_for_datasource_access(qs, current_user)
    return session.scalars(qs).all()


@router.post(
    "",
    response_model=schemas.DataSourceOut,
    status_code=201,
    dependencies=[
        Depends(require_leader),
        Depends(require_role(UserRole.admin, UserRole.edit)),
    ],
)
def create_data_source(
    data: schemas.DataSourceCreate,
    current_user: CurrentUser,
    session: SessionDep,
):
    src = session.get(models.StorageSource, data.storage_source_id)
    if src is None:
        raise HTTPException(
            status_code=400, detail="storage_source_id does not exist"
        )
    _enforce_credentials_for_remote_backends(
        src, data.access_key_id, data.secret_access_key,
    )
    # Per-owner uniqueness is enforced by a composite index created in
    # _migrate(); let SQLAlchemy raise IntegrityError and translate.
    ds = DataSource(
        name=data.name,
        storage_source_id=data.storage_source_id,
        path=data.path,
        access_key_id=data.access_key_id,
        secret_access_key=data.secret_access_key,
        description=data.description,
        owner_user_id=current_user.id,
    )
    session.add(ds)
    try:
        session.commit()
    except Exception as exc:  # IntegrityError from uq_data_sources_owner_name
        session.rollback()
        raise HTTPException(
            status_code=409,
            detail=f"data source name already exists for this owner: {exc}",
        ) from exc
    session.refresh(ds)
    return ds


@router.get(
    "/{data_source_id}",
    response_model=schemas.DataSourceOut,
)
def get_data_source(
    data_source_id: int,
    current_user: CurrentUser,
    session: SessionDep,
):
    ds = session.get(DataSource, data_source_id)
    if ds is None:
        raise HTTPException(status_code=404, detail="data source not found")
    if not check_datasource_access(current_user, ds, "read", session):
        raise HTTPException(
            status_code=403,
            detail={"error": "forbidden", "level": "read",
                    "reason": "no access to this data source"},
        )
    return ds


@router.put(
    "/{data_source_id}",
    response_model=schemas.DataSourceOut,
    dependencies=[Depends(require_datasource_access("write"))],
)
def update_data_source(
    data_source_id: int,
    data: schemas.DataSourceUpdate,
    session: SessionDep,
    ds: Annotated[DataSource, Depends(require_datasource_access("write"))],
):
    # Switching to a different storage_source_id is allowed but the FK must
    # exist.
    if data.storage_source_id is not None:
        src = session.get(models.StorageSource, data.storage_source_id)
        if src is None:
            raise HTTPException(
                status_code=400, detail="storage_source_id does not exist"
            )
        # If the user is changing source OR clearing AK/SK, re-validate.
        # Look at the *post-update* state: take unset fields as-is from `ds`.
        post_src = src
        post_ak = data.access_key_id if data.access_key_id is not None else ds.access_key_id
        post_sk = (
            data.secret_access_key
            if data.secret_access_key is not None
            else ds.secret_access_key
        )
        _enforce_credentials_for_remote_backends(post_src, post_ak, post_sk)
    elif data.access_key_id is not None or data.secret_access_key is not None:
        # AK/SK cleared or replaced on the *same* source — same rule.
        post_ak = data.access_key_id if data.access_key_id is not None else ds.access_key_id
        post_sk = (
            data.secret_access_key
            if data.secret_access_key is not None
            else ds.secret_access_key
        )
        _enforce_credentials_for_remote_backends(
            ds.storage_source, post_ak, post_sk
        )
    for key, value in data.model_dump(exclude_unset=True).items():
        setattr(ds, key, value)
    try:
        session.commit()
    except Exception as exc:
        session.rollback()
        raise HTTPException(
            status_code=409,
            detail=f"update conflict (likely duplicate name): {exc}",
        ) from exc
    session.refresh(ds)
    return ds


@router.delete(
    "/{data_source_id}",
    status_code=204,
    dependencies=[Depends(require_datasource_access("admin"))],
)
def delete_data_source(
    data_source_id: int,
    session: SessionDep,
    ds: Annotated[DataSource, Depends(require_datasource_access("admin"))],
):
    used = session.scalar(
        select(models.SyncTask).where(
            or_(
                models.SyncTask.src_data_source_id == data_source_id,
                models.SyncTask.dst_data_source_id == data_source_id,
            )
        ).limit(1)
    )
    if used is not None:
        raise HTTPException(
            status_code=409, detail="data source is used by a sync task"
        )
    used = session.scalar(
        select(models.CheckTask).where(
            or_(
                models.CheckTask.src_data_source_id == data_source_id,
                models.CheckTask.dst_data_source_id == data_source_id,
            )
        ).limit(1)
    )
    if used is not None:
        raise HTTPException(
            status_code=409, detail="data source is used by a check task"
        )
    session.delete(ds)
    session.commit()


# --- /verify --------------------------------------------------------------


@router.post(
    "/{data_source_id}/verify",
    response_model=schemas.DataSourceVerifyOut,
)
def verify_data_source(
    data_source_id: int,
    request: Request,
    current_user: CurrentUser,
    session: SessionDep,
):
    """Probe read + write access by pushing the remote and calling rclone.

    Updates ``last_verified_at`` + ``last_verified_ok`` on the data source.
    Does **not** remove the remote from rclone — the data source stays
    usable for sync tasks.
    """
    ds = session.get(DataSource, data_source_id)
    if ds is None:
        raise HTTPException(status_code=404, detail="data source not found")
    if not check_datasource_access(current_user, ds, "write", session):
        raise HTTPException(
            status_code=403,
            detail={"error": "forbidden", "level": "write",
                    "reason": "no binding to this data source"},
        )
    src = session.get(models.StorageSource, ds.storage_source_id)
    if src is None:
        raise HTTPException(
            status_code=500, detail="data source has no storage source"
        )
    client = getattr(request.app.state, "rclone_client", None)
    if client is None:
        raise HTTPException(
            status_code=503, detail="rclone client not available"
        )

    remote_name = _ds_remote_name(ds)
    remote_spec = f"{remote_name}:{ds.path}"
    error_msg: str | None = None
    read_ok = False
    write_ok = False

    # Push the remote (idempotent: ensure_remote swallows "already exists")
    try:
        client.create_remote(remote_name, src.type, build_remote_parameters(src, ds))
    except RcloneApiError as exc:
        # Not fatal yet — about() may still work if the remote was already pushed.
        error_msg = f"create_remote: {exc}"

    try:
        client.list(remote_spec)
        read_ok = True
    except RcloneApiError as exc:
        error_msg = f"list: {exc}"

    if read_ok:
        try:
            ok, werr = client.write_probe(remote_spec)
        except RcloneApiError as exc:
            ok, werr = False, str(exc)
        write_ok = ok
        if werr:
            # Either write failed (write_ok=False) or cleanup failed (write_ok=True).
            tag = "write" if not ok else "write cleanup"
            error_msg = f"{tag}: {werr}"

    now = datetime.now(timezone.utc)
    ds.last_verified_at = now
    ds.last_verified_ok = bool(read_ok and write_ok and error_msg is None)
    session.commit()

    return schemas.DataSourceVerifyOut(
        read_ok=read_ok,
        write_ok=write_ok,
        error=error_msg,
        probed_at=now,
    )


# --- /bindings ------------------------------------------------------------


@router.get(
    "/{data_source_id}/bindings",
    response_model=list[schemas.DataSourceBindingOut],
)
def list_bindings(
    data_source_id: int,
    current_user: CurrentUser,
    session: SessionDep,
):
    ds = session.get(DataSource, data_source_id)
    if ds is None:
        raise HTTPException(status_code=404, detail="data source not found")
    if not check_datasource_access(current_user, ds, "read", session):
        raise HTTPException(
            status_code=403,
            detail={"error": "forbidden", "level": "read",
                    "reason": "no access to this data source"},
        )
    return session.scalars(
        select(DataSourceBinding)
        .where(DataSourceBinding.data_source_id == data_source_id)
        .order_by(DataSourceBinding.id)
    ).all()


@router.post(
    "/{data_source_id}/bindings",
    response_model=schemas.DataSourceBindingOut,
    status_code=201,
    dependencies=[Depends(require_datasource_access("admin"))],
)
def create_binding(
    data_source_id: int,
    data: schemas.DataSourceBindingCreate,
    session: SessionDep,
    current_user: CurrentUser,
    _ds: Annotated[DataSource, Depends(require_datasource_access("admin"))],
):
    user = session.get(User, data.user_id)
    if user is None:
        raise HTTPException(status_code=400, detail="user_id does not exist")
    binding = DataSourceBinding(
        data_source_id=data_source_id,
        user_id=data.user_id,
        permission=BindingPermission(data.permission),
        created_by_user_id=current_user.id,
    )
    session.add(binding)
    try:
        session.commit()
    except Exception as exc:
        session.rollback()
        raise HTTPException(
            status_code=409, detail=f"binding already exists: {exc}"
        ) from exc
    session.refresh(binding)
    return binding


@router.put(
    "/{data_source_id}/bindings/{binding_id}",
    response_model=schemas.DataSourceBindingOut,
    dependencies=[Depends(require_datasource_access("admin"))],
)
def update_binding(
    data_source_id: int,
    binding_id: int,
    data: schemas.DataSourceBindingUpdate,
    session: SessionDep,
):
    binding = session.get(DataSourceBinding, binding_id)
    if binding is None or binding.data_source_id != data_source_id:
        raise HTTPException(status_code=404, detail="binding not found")
    binding.permission = BindingPermission(data.permission)
    session.commit()
    session.refresh(binding)
    return binding


@router.delete(
    "/{data_source_id}/bindings/{binding_id}",
    status_code=204,
    dependencies=[Depends(require_datasource_access("admin"))],
)
def delete_binding(
    data_source_id: int,
    binding_id: int,
    session: SessionDep,
):
    binding = session.get(DataSourceBinding, binding_id)
    if binding is None or binding.data_source_id != data_source_id:
        raise HTTPException(status_code=404, detail="binding not found")
    session.delete(binding)
    session.commit()
