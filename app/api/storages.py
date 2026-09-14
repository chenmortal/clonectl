"""Legacy ``StorageConfig`` API — DEPRECATED writes.

GET endpoints remain for the deprecation window so existing external
clients keep working. POST/PUT/DELETE return ``410 Gone`` and instruct
clients to migrate to ``/api/storage-sources`` and ``/api/data-sources``.
"""
from __future__ import annotations

from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy import select
from sqlalchemy.orm import Session

from app import models, schemas
from app.auth.deps import require_role
from app.db import get_session
from app.models import UserRole

router = APIRouter(
    prefix="/api/storages",
    tags=["storages"],
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)

SessionDep = Annotated[Session, Depends(get_session)]


_DEPRECATED = {
    "error": "deprecated",
    "use": "/api/storage-sources and /api/data-sources",
}


@router.get("", response_model=list[schemas.StorageOut])
def list_storages(session: SessionDep):
    return session.scalars(select(models.StorageConfig)).all()


@router.post(
    "",
    response_model=schemas.StorageOut,
    status_code=status.HTTP_410_GONE,
)
def create_storage_deprecated():
    """Deprecated. Use ``POST /api/storage-sources`` then
    ``POST /api/data-sources``."""
    raise HTTPException(status_code=status.HTTP_410_GONE, detail=_DEPRECATED)


@router.get("/{storage_id}", response_model=schemas.StorageOut)
def get_storage(storage_id: int, session: SessionDep):
    storage = session.get(models.StorageConfig, storage_id)
    if storage is None:
        raise HTTPException(status_code=404, detail="storage not found")
    return storage


@router.put(
    "/{storage_id}",
    response_model=schemas.StorageOut,
    status_code=status.HTTP_410_GONE,
)
def update_storage_deprecated():
    """Deprecated. Use ``PUT /api/storage-sources/{id}`` or
    ``PUT /api/data-sources/{id}`` as appropriate."""
    raise HTTPException(status_code=status.HTTP_410_GONE, detail=_DEPRECATED)


@router.delete(
    "/{storage_id}",
    status_code=status.HTTP_410_GONE,
)
def delete_storage_deprecated():
    """Deprecated. Use ``DELETE /api/storage-sources/{id}`` or
    ``DELETE /api/data-sources/{id}`` as appropriate."""
    raise HTTPException(status_code=status.HTTP_410_GONE, detail=_DEPRECATED)
