"""Password hashing helpers backed by bcrypt."""
from __future__ import annotations

import bcrypt

from app.config import get_settings


def hash_password(plain: str) -> str:
    """Return a bcrypt hash of ``plain`` using the configured cost factor."""
    rounds = get_settings().bcrypt_rounds
    salt = bcrypt.gensalt(rounds=rounds)
    return bcrypt.hashpw(plain.encode("utf-8"), salt).decode("utf-8")


def verify_password(plain: str, hashed: str) -> bool:
    """Constant-time check. Returns False on any decode/format error."""
    try:
        return bcrypt.checkpw(plain.encode("utf-8"), hashed.encode("utf-8"))
    except (ValueError, TypeError):
        return False
