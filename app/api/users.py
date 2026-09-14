"""Admin-only user management routes."""
from __future__ import annotations

from datetime import datetime
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.auth.deps import CurrentUser, require_role
from app.auth.security import hash_password
from app.db import get_session
from app.models import User, UserRole
from app.schemas import ResetPasswordIn, UserCreate, UserOut, UserUpdate

router = APIRouter(
    prefix="/api/users",
    tags=["users"],
    dependencies=[Depends(require_role(UserRole.admin))],
)

SessionDep = Annotated[Session, Depends(get_session)]


def _parse_role(raw: str) -> UserRole:
    try:
        return UserRole(raw)
    except ValueError as exc:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail={"error": "invalid_role", "value": raw, "allowed": [r.value for r in UserRole]},
        ) from exc


@router.get("", response_model=list[UserOut])
def list_users(session: SessionDep) -> list[UserOut]:
    rows = session.query(User).order_by(User.id).all()
    return [UserOut.model_validate(r) for r in rows]


@router.post("", response_model=UserOut, status_code=status.HTTP_201_CREATED)
def create_user(data: UserCreate, session: SessionDep) -> UserOut:
    if session.query(User).filter(User.username == data.username).first() is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail={"error": "username_taken", "username": data.username},
        )
    role = _parse_role(data.role)
    user = User(
        username=data.username,
        password_hash=hash_password(data.password),
        role=role,
    )
    session.add(user)
    session.commit()
    session.refresh(user)
    return UserOut.model_validate(user)


@router.put("/{user_id}", response_model=UserOut)
def update_user(user_id: int, data: UserUpdate, session: SessionDep) -> UserOut:
    user = session.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="user not found")
    if data.role is not None:
        user.role = _parse_role(data.role)
    if data.disabled is not None:
        user.disabled_at = datetime.utcnow() if data.disabled else None
    session.commit()
    session.refresh(user)
    return UserOut.model_validate(user)


@router.delete("/{user_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_user(user_id: int, current_user: CurrentUser, session: SessionDep) -> None:
    if user_id == current_user.id:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail={"error": "cannot_delete_self"},
        )
    user = session.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="user not found")
    session.delete(user)
    session.commit()


@router.post(
    "/{user_id}/reset-password",
    status_code=status.HTTP_204_NO_CONTENT,
)
def reset_password(user_id: int, data: ResetPasswordIn, session: SessionDep) -> None:
    user = session.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="user not found")
    user.password_hash = hash_password(data.new_password)
    session.commit()
