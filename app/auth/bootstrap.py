"""Idempotent admin bootstrap from environment variables."""
from __future__ import annotations

import logging

from sqlalchemy import func, select
from sqlalchemy.orm import sessionmaker

from app.auth.security import hash_password
from app.config import Settings
from app.models import User, UserRole

logger = logging.getLogger(__name__)


def bootstrap_admin(session_factory: sessionmaker, settings: Settings) -> None:
    """Create the bootstrap admin iff users table is empty and both env vars are set.

    Safe to call on every startup; no-op when:
      * users table already has any row, or
      * either env var is missing/blank.
    """
    user = settings.bootstrap_admin_user.strip()
    password = settings.bootstrap_admin_password
    if not user or not password:
        logger.debug("bootstrap_admin: env vars not set, skipping")
        return

    with session_factory() as session:
        existing_count = session.scalar(select(func.count()).select_from(User)) or 0
        if existing_count > 0:
            logger.debug(
                "bootstrap_admin: users table not empty (%d rows), skipping",
                existing_count,
            )
            return

        admin = User(
            username=user,
            password_hash=hash_password(password),
            role=UserRole.admin,
        )
        session.add(admin)
        session.commit()
        logger.warning(
            "bootstrap_admin: created admin user %r (id=%d). "
            "Change the password via POST /api/auth/change-password as soon as possible.",
            admin.username,
            admin.id,
        )
