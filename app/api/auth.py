"""Authentication routes: login, current-user probe, change-password."""
from __future__ import annotations

from datetime import datetime
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.auth.deps import CurrentUser
from app.auth.security import hash_password, verify_password
from app.auth.tokens import create_access_token
from app.config import get_settings
from app.db import get_session
from app.models import User
from app.schemas import ChangePasswordIn, LoginIn, MeOut, TokenOut

router = APIRouter(prefix="/api/auth", tags=["auth"])

SessionDep = Annotated[Session, Depends(get_session)]


def _authenticate(session: Session, username: str, password: str) -> User | None:
    """Return the user iff username+password match AND account is enabled."""
    user = session.query(User).filter(User.username == username).one_or_none()
    if user is None or user.disabled_at is not None:
        return None
    if not verify_password(password, user.password_hash):
        return None
    return user


@router.post("/login", response_model=TokenOut)
def login(data: LoginIn, session: SessionDep) -> TokenOut:
    """Issue a JWT for valid credentials. Unknown user, bad password, and disabled
    account all collapse to the same 401 so we don't leak account existence."""
    user = _authenticate(session, data.username, data.password)
    if user is None:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "invalid_credentials"},
            headers={"WWW-Authenticate": "Bearer"},
        )
    user.last_login_at = datetime.utcnow()
    session.commit()
    settings = get_settings()
    return TokenOut(
        access_token=create_access_token(
            user_id=user.id, username=user.username, role=user.role.value
        ),
        expires_in=settings.jwt_expires_minutes * 60,
        role=user.role.value,
    )


@router.get("/me", response_model=MeOut)
def me(current_user: CurrentUser) -> MeOut:
    return MeOut.model_validate(current_user)


@router.post("/change-password", status_code=status.HTTP_204_NO_CONTENT)
def change_password(
    data: ChangePasswordIn,
    current_user: CurrentUser,
    session: SessionDep,
) -> None:
    """Re-authenticate with old_password, then rotate the hash."""
    db_user = session.get(User, current_user.id)
    assert db_user is not None  # get_current_user guarantees this
    if not verify_password(data.old_password, db_user.password_hash):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"error": "invalid_credentials", "reason": "old_password mismatch"},
        )
    db_user.password_hash = hash_password(data.new_password)
    session.commit()
