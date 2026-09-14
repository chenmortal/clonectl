from collections.abc import Generator
from datetime import datetime

from sqlalchemy import create_engine, inspect, text
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker

from app.config import get_settings


class Base(DeclarativeBase):
    pass


def build_engine(database_url: str | None = None):
    url = database_url or get_settings().database_url
    kwargs = {}
    if url.startswith("sqlite"):
        kwargs["connect_args"] = {"check_same_thread": False}
    return create_engine(url, **kwargs)


engine = build_engine()
session_factory = sessionmaker(bind=engine, expire_on_commit=False)


def init_db() -> None:
    from app import models  # noqa: F401

    Base.metadata.create_all(engine)
    _migrate()


def _table_cols(conn, table: str) -> set[str]:
    """Helper: column names that exist on ``table`` in the current DB."""
    rows = conn.execute(text(f"PRAGMA table_info({table})")).fetchall()  # SQLite
    return {row[1] for row in rows}


def _index_names(conn, table: str) -> set[str]:
    rows = conn.execute(text(f"PRAGMA index_list({table})")).fetchall()
    return {row[1] for row in rows}


def _column_notnull(conn, table: str, column: str) -> bool:
    """Return True if ``column`` on ``table`` is declared NOT NULL (SQLite)."""
    rows = conn.execute(text(f"PRAGMA table_info({table})")).fetchall()
    # row = (cid, name, type, notnull, dflt_value, pk)
    for row in rows:
        if row[1] == column:
            return bool(row[3])
    return False


def _rebuild_nullable_storage_fks(conn, table: str) -> None:
    """SQLite has no ``ALTER COLUMN ... DROP NOT NULL``. Rebuild the table
    with ``src_storage_id`` / ``dst_storage_id`` declared nullable so the
    migration can NULL them out.

    Used to make way for the v2 ``src_data_source_id`` / ``dst_data_source_id``
    pair to take over. No-op on dialects that already accepted the in-place
    ALTER (MySQL)."""
    need_src = _column_notnull(conn, table, "src_storage_id")
    need_dst = _column_notnull(conn, table, "dst_storage_id")
    if not need_src and not need_dst:
        return  # already nullable; nothing to do

    new = f"{table}__new"
    if table == "sync_tasks":
        cols_new = (
            "id", "name", "src_storage_id", "src_path", "dst_storage_id",
            "src_data_source_id", "dst_data_source_id", "dst_path", "mode",
            "cron", "enabled", "rclone_options", "pre_check_task_id",
            "created_at", "updated_at",
        )
        conn.execute(text(
            f"""
            CREATE TABLE {new} (
                id INTEGER PRIMARY KEY,
                name VARCHAR(128) NOT NULL,
                src_storage_id INTEGER REFERENCES storage_configs(id),
                src_path VARCHAR(512) NOT NULL,
                dst_storage_id INTEGER REFERENCES storage_configs(id),
                src_data_source_id INTEGER REFERENCES data_sources(id),
                dst_data_source_id INTEGER REFERENCES data_sources(id),
                dst_path VARCHAR(512) NOT NULL,
                mode VARCHAR NOT NULL,
                cron VARCHAR(128) NOT NULL,
                enabled BOOLEAN NOT NULL,
                rclone_options JSON NOT NULL,
                pre_check_task_id INTEGER REFERENCES check_tasks(id),
                created_at DATETIME,
                updated_at DATETIME
            )
            """
        ))
    else:  # check_tasks
        cols_new = (
            "id", "name", "src_storage_id", "src_path", "dst_storage_id",
            "src_data_source_id", "dst_data_source_id", "dst_path", "cron",
            "enabled", "check_options", "created_at", "updated_at",
        )
        conn.execute(text(
            f"""
            CREATE TABLE {new} (
                id INTEGER PRIMARY KEY,
                name VARCHAR(128) NOT NULL,
                src_storage_id INTEGER REFERENCES storage_configs(id),
                src_path VARCHAR(512) NOT NULL,
                dst_storage_id INTEGER REFERENCES storage_configs(id),
                src_data_source_id INTEGER REFERENCES data_sources(id),
                dst_data_source_id INTEGER REFERENCES data_sources(id),
                dst_path VARCHAR(512) NOT NULL,
                cron VARCHAR(128),
                enabled BOOLEAN NOT NULL,
                check_options JSON NOT NULL,
                created_at DATETIME,
                updated_at DATETIME
            )
            """
        ))
    # Copy data column-by-column. ``SELECT *`` order depends on the original
    # CREATE TABLE layout, which may not match our target. Use an explicit
    # column list with ``COALESCE`` so newly-introduced columns default to
    # NULL when absent on the source table.
    src_cols = {row[1] for row in conn.execute(
        text(f"PRAGMA table_info({table})")
    ).fetchall()}
    select_exprs = [
        f'COALESCE({col}, NULL)' if col in src_cols else 'NULL'
        for col in cols_new
    ]
    cols_csv = ", ".join(cols_new)
    select_csv = ", ".join(select_exprs)
    conn.execute(text(
        f"INSERT INTO {new} ({cols_csv}) SELECT {select_csv} FROM {table}"
    ))
    conn.execute(text(f"DROP TABLE {table}"))
    conn.execute(text(f"ALTER TABLE {new} RENAME TO {table}"))
    # Recreate the data_source FK indexes (the original ones were dropped
    # with the table). Storage FK indexes weren't there originally so no
    # need to recreate.
    conn.execute(text(
        f"CREATE INDEX IF NOT EXISTS ix_{table}_src_data_source_id "
        f"ON {table}(src_data_source_id)"
    ))
    conn.execute(text(
        f"CREATE INDEX IF NOT EXISTS ix_{table}_dst_data_source_id "
        f"ON {table}(dst_data_source_id)"
    ))


# Column type/constraint definitions for ``_rebuild_nullable_*`` rebuilds.
# Keys must match the original CREATE TABLE column order; the rebuild
# script only carries forward columns listed here that also exist in the
# source table.

_SYNC_TASK_COLS_DEF = {
    "id": "INTEGER PRIMARY KEY",
    "name": "VARCHAR(128) NOT NULL",
    "src_storage_id": "INTEGER REFERENCES storage_configs(id)",
    "src_path": "VARCHAR(512) NOT NULL",
    "dst_storage_id": "INTEGER REFERENCES storage_configs(id)",
    "src_data_source_id": "INTEGER REFERENCES data_sources(id)",
    "dst_data_source_id": "INTEGER REFERENCES data_sources(id)",
    "dst_path": "VARCHAR(512) NOT NULL",
    "mode": "VARCHAR NOT NULL",
    "cron": "VARCHAR(128) NOT NULL",
    "enabled": "BOOLEAN NOT NULL DEFAULT 1",
    "rclone_options": "JSON NOT NULL DEFAULT '{}'",
    "pre_check_task_id": "INTEGER REFERENCES check_tasks(id)",
    "created_at": "DATETIME DEFAULT CURRENT_TIMESTAMP",
    "updated_at": "DATETIME DEFAULT CURRENT_TIMESTAMP",
}

_CHECK_TASK_COLS_DEF = {
    "id": "INTEGER PRIMARY KEY",
    "name": "VARCHAR(128) NOT NULL",
    "src_storage_id": "INTEGER REFERENCES storage_configs(id)",
    "src_path": "VARCHAR(512) NOT NULL",
    "dst_storage_id": "INTEGER REFERENCES storage_configs(id)",
    "src_data_source_id": "INTEGER REFERENCES data_sources(id)",
    "dst_data_source_id": "INTEGER REFERENCES data_sources(id)",
    "dst_path": "VARCHAR(512) NOT NULL",
    "cron": "VARCHAR(128)",
    "enabled": "BOOLEAN NOT NULL DEFAULT 1",
    "check_options": "JSON NOT NULL DEFAULT '{}'",
    "created_at": "DATETIME DEFAULT CURRENT_TIMESTAMP",
    "updated_at": "DATETIME DEFAULT CURRENT_TIMESTAMP",
}

_DATA_SOURCE_COLS_DEF = {
    "id": "INTEGER PRIMARY KEY",
    "name": "VARCHAR(128) NOT NULL",
    "storage_source_id": "INTEGER NOT NULL REFERENCES storage_sources(id)",
    "path": "VARCHAR(512) NOT NULL",
    "access_key_id": "VARCHAR(255)",          # NULL after v3
    "secret_access_key": "VARCHAR(512)",      # NULL after v3
    "description": "VARCHAR(512)",
    "owner_user_id": "INTEGER NOT NULL REFERENCES users(id)",
    "last_verified_at": "DATETIME",
    "last_verified_ok": "BOOLEAN",
    # DEFAULTs mirror the SQLAlchemy model (``server_default=func.now()``).
    # Without these, a rebuild leaves created_at / updated_at NULL and new
    # rows have no auto-timestamp, which then trips the response model
    # ``datetime_type`` validation in the API.
    "created_at": "DATETIME DEFAULT CURRENT_TIMESTAMP",
    "updated_at": "DATETIME DEFAULT CURRENT_TIMESTAMP",
}


def _rebuild_nullable_data_source_column(conn, column: str) -> None:
    """SQLite table-rebuild helper that drops NOT NULL on a single
    ``data_sources`` column. Used for the v3 AK/SK nullability relaxation.
    """
    if not _column_notnull(conn, "data_sources", column):
        return  # already nullable

    new = "data_sources__new"
    src_cols = {row[1] for row in conn.execute(
        text("PRAGMA table_info(data_sources)")
    ).fetchall()}
    selected = [c for c in _DATA_SOURCE_COLS_DEF.keys() if c in src_cols]
    cols_sql = ", ".join(
        f"{c} {_DATA_SOURCE_COLS_DEF[c]}" for c in selected
    )
    cols_csv = ", ".join(selected)
    conn.execute(text(f"CREATE TABLE {new} ({cols_sql})"))
    conn.execute(text(
        f"INSERT INTO {new} ({cols_csv}) SELECT {cols_csv} FROM data_sources"
    ))
    conn.execute(text("DROP TABLE data_sources"))
    conn.execute(text(f"ALTER TABLE {new} RENAME TO data_sources"))
    # Backfill NULL created_at / updated_at on legacy rows that pre-date the
    # DEFAULT clause — otherwise the API response model rejects them with
    # ``datetime_type``. Use SQLite's CURRENT_TIMESTAMP, which the new
    # column default also points to.
    conn.execute(text(
        "UPDATE data_sources SET created_at = CURRENT_TIMESTAMP "
        "WHERE created_at IS NULL"
    ))
    conn.execute(text(
        "UPDATE data_sources SET updated_at = CURRENT_TIMESTAMP "
        "WHERE updated_at IS NULL"
    ))
    # Recreate indexes that were dropped with the table.
    conn.execute(text(
        "CREATE INDEX IF NOT EXISTS ix_data_sources_storage_source_id "
        "ON data_sources (storage_source_id)"
    ))
    conn.execute(text(
        "CREATE INDEX IF NOT EXISTS ix_data_sources_owner_user_id "
        "ON data_sources (owner_user_id)"
    ))


def _migrate() -> None:
    inspector = inspect(engine)
    if not inspector.has_table("sync_tasks"):
        # Fresh DB; create_all above has already created all tables.
        return

    with engine.begin() as conn:
        # --- legacy: pre_check_task_id (already existed before this branch) ---
        cols = _table_cols(conn, "sync_tasks")
        if "pre_check_task_id" not in cols:
            conn.execute(
                text("ALTER TABLE sync_tasks ADD COLUMN pre_check_task_id INTEGER")
            )

        # --- legacy: split check_before_sync out of sync_tasks (very old schema) ---
        if "check_before_sync" in cols:
            _split_legacy_check_config(conn)

        # --- v2: relax src_storage_id / dst_storage_id nullability ---
        # SQLite path: rebuild the table (no DROP NOT NULL on this dialect).
        # MySQL path: ALTER ... MODIFY ... NULL (handled by a future PR;
        # the dev DB is SQLite, so we ship the SQLite form first).
        for tbl in ("sync_tasks", "check_tasks"):
            _rebuild_nullable_storage_fks(conn, tbl)

        # --- v3: relax data_sources.access_key_id / secret_access_key ---
        # Same SQLite-rebuild approach. local-FS data sources (rclone's
        # ``local`` backend) don't carry credentials; making the columns
        # nullable removes a vestigial NOT NULL constraint left over from
        # when every data source was an object-storage bucket.
        for col in ("access_key_id", "secret_access_key"):
            _rebuild_nullable_data_source_column(conn, col)

        # --- v2: dual storage / data_source FKs on sync_tasks + check_tasks ---
        # Re-check columns after rebuild (the rebuild re-created the table
        # including any new columns we'd already added; this is a no-op then).
        for tbl in ("sync_tasks", "check_tasks"):
            cols = _table_cols(conn, tbl)
            for col in ("src_data_source_id", "dst_data_source_id"):
                if col not in cols:
                    conn.execute(
                        text(f"ALTER TABLE {tbl} ADD COLUMN {col} INTEGER")
                    )

        # --- v2: alert dedup column on runs ---
        for tbl in ("sync_runs", "check_runs"):
            cols = _table_cols(conn, tbl)
            if "notified_at" not in cols:
                conn.execute(
                    text(f"ALTER TABLE {tbl} ADD COLUMN notified_at DATETIME")
                )

        # --- v2: composite unique (owner_user_id, name) on data_sources ---
        idx = _index_names(conn, "data_sources")
        if "uq_data_sources_owner_name" not in idx:
            try:
                conn.execute(
                    text(
                        "CREATE UNIQUE INDEX uq_data_sources_owner_name "
                        "ON data_sources (owner_user_id, name)"
                    )
                )
            except SQLAlchemyError:
                # Pre-existing duplicate rows from earlier dev — log & skip.
                pass

        # --- v2: composite unique (data_source_id, user_id) on bindings ---
        idx = _index_names(conn, "data_source_bindings")
        if "uq_data_source_bindings_ds_user" not in idx:
            try:
                conn.execute(
                    text(
                        "CREATE UNIQUE INDEX uq_data_source_bindings_ds_user "
                        "ON data_source_bindings (data_source_id, user_id)"
                    )
                )
            except SQLAlchemyError:
                pass


def _split_legacy_check_config(conn) -> None:
    rows = conn.execute(
        text(
            "SELECT id, name, src_storage_id, src_path, dst_storage_id, dst_path, enabled, "
            "check_cron, check_options, check_before_sync FROM sync_tasks "
            "WHERE check_cron IS NOT NULL OR check_before_sync = 1"
        )
    ).fetchall()
    now = datetime.utcnow()
    for row in rows:
        result = conn.execute(
            text(
                "INSERT INTO check_tasks (name, src_storage_id, src_path, dst_storage_id, "
                "dst_path, cron, enabled, check_options, created_at, updated_at) "
                "VALUES (:name, :src_id, :src_path, :dst_id, :dst_path, :cron, :enabled, "
                ":options, :now, :now)"
            ),
            {
                "name": f"{row.name}-检查",
                "src_id": row.src_storage_id,
                "src_path": row.src_path,
                "dst_id": row.dst_storage_id,
                "dst_path": row.dst_path,
                "cron": row.check_cron,
                "enabled": row.enabled,
                "options": row.check_options,
                "now": now,
            },
        )
        check_task_id = result.lastrowid
        if row.check_before_sync:
            conn.execute(
                text("UPDATE sync_tasks SET pre_check_task_id = :cid WHERE id = :id"),
                {"cid": check_task_id, "id": row.id},
            )
        conn.execute(
            text("UPDATE check_runs SET task_id = :cid WHERE task_id = :tid"),
            {"cid": check_task_id, "tid": row.id},
        )
        conn.execute(
            text(
                "UPDATE sync_tasks SET check_before_sync = 0, check_cron = NULL WHERE id = :id"
            ),
            {"id": row.id},
        )
    for column in ("check_before_sync", "check_cron", "check_options"):
        try:
            conn.execute(text(f"ALTER TABLE sync_tasks DROP COLUMN {column}"))
        except SQLAlchemyError:
            pass


def get_session() -> Generator[Session, None, None]:
    with session_factory() as session:
        yield session
