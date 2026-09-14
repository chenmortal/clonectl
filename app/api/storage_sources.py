"""StorageSource API: admin-managed provider templates.

Replaces the ``type``+``endpoint`` part of the legacy ``StorageConfig`` flow.
All authenticated users can read; only admin can write. Storage sources are
referenced by :class:`DataSource` rows (many data sources per source).
"""
from __future__ import annotations

from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import or_, select
from sqlalchemy.orm import Session

from app import models, schemas
from app.api.deps import require_leader
from app.auth.deps import require_role
from app.db import get_session
from app.models import DataSource, UserRole

router = APIRouter(
    prefix="/api/storage-sources",
    tags=["storage-sources"],
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)

SessionDep = Annotated[Session, Depends(get_session)]

_WRITE_ROLE_DEPS = [
    Depends(require_leader),
    Depends(require_role(UserRole.admin)),
]


def _ds_remote_name(ds: models.DataSource) -> str:
    """The deterministic rclone-side remote name for a DataSource.

    StorageSources themselves don't get pushed to rclone as remotes — only
    DataSource instances (which carry AK/SK + path) do. This helper is
    used by the verify endpoint to push a temp remote.
    """
    return f"ds-{ds.id}"


def build_remote_parameters(source: models.StorageSource, ds: models.DataSource) -> dict:
    """Build the rclone ``parameters`` dict for a (source, ds) pair.

    Source-level extras are merged under the provider params; AK/SK + path
    are added on top. Returned dict is what gets sent to
    ``/config/create``.

    For ``type="local"`` (rclone's local-FS backend) credentials are
    ignored by rclone, so we deliberately omit them — pushing the
    DataSource's AK/SK into a local remote would just leak the values
    into the rcd config. ``extra.root`` (if set) is forwarded verbatim
    to scope the remote at config time.
    """
    params: dict = dict(source.extra or {})
    if source.endpoint:
        params["endpoint"] = source.endpoint
    if source.region:
        params["region"] = source.region
    if source.type == "local":
        # No AK/SK, no provider; ``root`` (if set in extra) is forwarded.
        # rclone's ``local`` backend defaults ``root`` to the rcd's CWD if
        # unset — that makes ``ds.path`` (typically an absolute path like
        # ``/var/data``) resolve to ``<cwd>/var/data`` and 404. Default to
        # ``/`` so ``ds-N:/abs/path`` is the literal host filesystem path;
        # admins can still scope by setting ``extra.root``.
        if "root" not in params:
            params["root"] = "/"
        return params
    if source.type == "s3":
        # rclone S3 requires explicit provider unless endpoint is set
        params.setdefault("provider", "Other")
    params["access_key_id"] = ds.access_key_id or ""
    params["secret_access_key"] = ds.secret_access_key or ""
    return params


@router.get("", response_model=list[schemas.StorageSourceOut])
def list_storage_sources(session: SessionDep):
    return session.scalars(select(models.StorageSource).order_by(models.StorageSource.id)).all()


@router.post(
    "",
    response_model=schemas.StorageSourceOut,
    status_code=201,
    dependencies=_WRITE_ROLE_DEPS,
)
def create_storage_source(
    data: schemas.StorageSourceCreate, session: SessionDep
):
    exists = session.scalar(
        select(models.StorageSource).where(models.StorageSource.name == data.name)
    )
    if exists:
        raise HTTPException(status_code=409, detail="storage source name already exists")
    src = models.StorageSource(**data.model_dump())
    session.add(src)
    session.commit()
    session.refresh(src)
    return src


@router.get("/{source_id}", response_model=schemas.StorageSourceOut)
def get_storage_source(source_id: int, session: SessionDep):
    src = session.get(models.StorageSource, source_id)
    if src is None:
        raise HTTPException(status_code=404, detail="storage source not found")
    return src


@router.put(
    "/{source_id}",
    response_model=schemas.StorageSourceOut,
    dependencies=_WRITE_ROLE_DEPS,
)
def update_storage_source(
    source_id: int, data: schemas.StorageSourceUpdate, session: SessionDep
):
    src = session.get(models.StorageSource, source_id)
    if src is None:
        raise HTTPException(status_code=404, detail="storage source not found")
    for key, value in data.model_dump(exclude_unset=True).items():
        setattr(src, key, value)
    session.commit()
    session.refresh(src)
    return src


@router.delete(
    "/{source_id}",
    status_code=204,
    dependencies=_WRITE_ROLE_DEPS,
)
def delete_storage_source(source_id: int, session: SessionDep):
    src = session.get(models.StorageSource, source_id)
    if src is None:
        raise HTTPException(status_code=404, detail="storage source not found")
    used = session.scalar(
        select(DataSource).where(DataSource.storage_source_id == source_id).limit(1)
    )
    if used is not None:
        raise HTTPException(
            status_code=409,
            detail="storage source is used by a data source; delete those first",
        )
    session.delete(src)
    session.commit()
