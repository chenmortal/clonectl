"""FastAPI dependencies for authentication and RBAC."""
from __future__ import annotations

from collections.abc import Callable
from typing import Annotated, Literal

from fastapi import Depends, HTTPException, status
from fastapi.security import OAuth2PasswordBearer
from sqlalchemy import Select, or_, select
from sqlalchemy.orm import Session

from app.auth.tokens import InvalidTokenError, decode_access_token
from app.db import get_session
from app.models import (
    BindingPermission,
    DataSource,
    DataSourceBinding,
    User,
    UserRole,
)

# tokenUrl is referenced by Swagger UI's "Authorize" widget.
# auto_error=False so missing/invalid tokens flow through get_current_user and
# produce a uniform 401, rather than OAuth2PasswordBearer defaulting to 403.
oauth2_scheme = OAuth2PasswordBearer(tokenUrl="/api/auth/login", auto_error=False)


def get_current_user(
    token: Annotated[str | None, Depends(oauth2_scheme)],
    session: Annotated[Session, Depends(get_session)],
) -> User:
    """Resolve the bearer token to a live, non-disabled User row."""
    if not token:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "unauthenticated", "reason": "missing bearer token"},
            headers={"WWW-Authenticate": "Bearer"},
        )
    try:
        payload = decode_access_token(token)
    except InvalidTokenError as exc:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "unauthenticated", "reason": f"invalid token: {exc}"},
            headers={"WWW-Authenticate": "Bearer"},
        ) from exc

    user_id_raw = payload.get("sub")
    if not user_id_raw:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "unauthenticated", "reason": "token missing sub"},
            headers={"WWW-Authenticate": "Bearer"},
        )
    try:
        user_id = int(user_id_raw)
    except (TypeError, ValueError):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "unauthenticated", "reason": "token sub not an int"},
            headers={"WWW-Authenticate": "Bearer"},
        ) from None

    user = session.get(User, user_id)
    if user is None or user.disabled_at is not None:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "unauthenticated", "reason": "user not found or disabled"},
            headers={"WWW-Authenticate": "Bearer"},
        )
    return user


def require_role(*allowed: UserRole) -> Callable[[User], User]:
    """Build a dep that 403s when ``current_user.role`` isn't in ``allowed``."""

    allowed_set = set(allowed)

    def _dep(current_user: Annotated[User, Depends(get_current_user)]) -> User:
        if current_user.role not in allowed_set:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail={
                    "error": "forbidden",
                    "required": sorted(r.value for r in allowed_set),
                    "actual": current_user.role.value,
                },
            )
        return current_user

    return _dep


CurrentUser = Annotated[User, Depends(get_current_user)]


# ---------------------------------------------------------------------------
# DataSource RBAC (per-resource, binding-based)
# ---------------------------------------------------------------------------

DataSourceAccessLevel = Literal["read", "write", "admin"]

# Permission ordering: admin ⊇ write ⊇ read.
_PERMISSION_RANK: dict[BindingPermission, int] = {
    BindingPermission.read: 1,
    BindingPermission.write: 2,
    BindingPermission.admin: 3,
}


def check_datasource_access(
    user: User,
    ds: DataSource,
    level: DataSourceAccessLevel,
    session: Session,
) -> bool:
    """Return True iff ``user`` may access ``ds`` at ``level``.

    Rules (first match wins):
    1. ``user.role == admin`` → True.
    2. ``level in ("write","admin") and ds.owner_user_id == user.id`` → True.
       (Owners always have admin on their own data sources.)
    3. DataSourceBinding row for (ds.id, user.id) with permission rank ≥ level rank.

    Read-level access for the owner is implicit (a user can see what they own).
    """
    if user.role == UserRole.admin:
        return True
    if ds.owner_user_id == user.id:
        # Owner has write/admin on their own resource. Read is implied.
        return True
    perm = session.scalar(
        select(DataSourceBinding.permission).where(
            DataSourceBinding.data_source_id == ds.id,
            DataSourceBinding.user_id == user.id,
        )
    )
    if perm is None:
        return False
    return _PERMISSION_RANK[perm] >= _PERMISSION_RANK[BindingPermission(level)]


def require_datasource_access(
    level: DataSourceAccessLevel,
) -> Callable[..., DataSource]:
    """Build a dep that loads a DataSource by id and 403s if not accessible.

    Returns the loaded ``DataSource`` so the route handler can use it directly.
    404 if the data source does not exist; 403 if access is denied.
    """

    def _dep(
        data_source_id: int,
        current_user: Annotated[User, Depends(get_current_user)],
        session: Annotated[Session, Depends(get_session)],
    ) -> DataSource:
        ds = session.get(DataSource, data_source_id)
        if ds is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail={"error": "not found", "resource": "data_source"},
            )
        if not check_datasource_access(current_user, ds, level, session):
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail={
                    "error": "forbidden",
                    "level": level,
                    "reason": "no binding to this data source",
                },
            )
        return ds

    return _dep


def filter_query_for_datasource_access(
    qs: Select, user: User
) -> Select:
    """Return ``qs`` filtered so only DataSources the user can ``read`` are kept.

    Admin → unrestricted. Non-admin → keep rows where
    (owner_user_id == user.id) OR (a DataSourceBinding exists for this user).
    """
    if user.role == UserRole.admin:
        return qs
    return qs.where(
        or_(
            DataSource.owner_user_id == user.id,
            DataSource.id.in_(
                select(DataSourceBinding.data_source_id).where(
                    DataSourceBinding.user_id == user.id
                )
            ),
        )
    )

