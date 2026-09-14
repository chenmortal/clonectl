import enum
from datetime import datetime

from sqlalchemy import (
    JSON,
    BigInteger,
    Boolean,
    DateTime,
    Enum,
    ForeignKey,
    Integer,
    String,
    Text,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db import Base


class SyncMode(str, enum.Enum):
    sync = "sync"
    copy = "copy"


class RunStatus(str, enum.Enum):
    pending = "pending"
    running = "running"
    success = "success"
    failed = "failed"
    skipped = "skipped"


class RunTrigger(str, enum.Enum):
    schedule = "schedule"
    manual = "manual"


class StorageConfig(Base):
    __tablename__ = "storage_configs"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(128), unique=True, nullable=False)
    type: Mapped[str] = mapped_column(String(64), nullable=False)
    parameters: Mapped[dict] = mapped_column(JSON, nullable=False, default=dict)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, server_default=func.now(), onupdate=func.now()
    )


class StorageSource(Base):
    """Provider template (S3 / OSS / COS / GCS / Azure / etc). Admin manages.

    Holds the immutable ``type`` + ``endpoint`` and any non-secret provider
    parameters. All authenticated users can read; only admin can write.
    """

    __tablename__ = "storage_sources"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(128), unique=True, nullable=False)
    type: Mapped[str] = mapped_column(String(64), nullable=False)
    endpoint: Mapped[str | None] = mapped_column(String(512), nullable=True)
    region: Mapped[str | None] = mapped_column(String(64), nullable=True)
    extra: Mapped[dict] = mapped_column(JSON, nullable=False, default=dict)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, server_default=func.now(), onupdate=func.now()
    )

    data_sources: Mapped[list["DataSource"]] = relationship(back_populates="storage_source")


class DataSource(Base):
    """End-user "bucket + path" bound to a StorageSource.

    Holds AK/SK + path. CRUD is open to admin and to users bound to this
    data source. Multiple users can be bound via DataSourceBinding.
    """

    __tablename__ = "data_sources"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    storage_source_id: Mapped[int] = mapped_column(
        ForeignKey("storage_sources.id", ondelete="RESTRICT"), nullable=False,
        index=True,
    )
    path: Mapped[str] = mapped_column(String(512), nullable=False)
    access_key_id: Mapped[str | None] = mapped_column(String(255), nullable=True)
    secret_access_key: Mapped[str | None] = mapped_column(String(512), nullable=True)
    description: Mapped[str | None] = mapped_column(String(512), nullable=True)
    owner_user_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="RESTRICT"), nullable=False, index=True,
    )
    last_verified_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    last_verified_ok: Mapped[bool | None] = mapped_column(Boolean, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, server_default=func.now(), onupdate=func.now()
    )

    storage_source: Mapped[StorageSource] = relationship(back_populates="data_sources")
    owner: Mapped["User"] = relationship(foreign_keys=[owner_user_id])
    bindings: Mapped[list["DataSourceBinding"]] = relationship(
        back_populates="data_source", cascade="all, delete-orphan"
    )


class BindingPermission(str, enum.Enum):
    read = "read"
    write = "write"
    admin = "admin"


class DataSourceBinding(Base):
    """M:N user ↔ DataSource with permission level."""

    __tablename__ = "data_source_bindings"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    data_source_id: Mapped[int] = mapped_column(
        ForeignKey("data_sources.id", ondelete="CASCADE"), nullable=False, index=True,
    )
    user_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="CASCADE"), nullable=False, index=True,
    )
    permission: Mapped[BindingPermission] = mapped_column(
        Enum(BindingPermission), nullable=False
    )
    created_by_user_id: Mapped[int | None] = mapped_column(
        ForeignKey("users.id", ondelete="SET NULL"), nullable=True,
    )
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())

    data_source: Mapped[DataSource] = relationship(back_populates="bindings")
    user: Mapped["User"] = relationship(foreign_keys=[user_id])


class SystemSetting(Base):
    """Singleton key/value system configuration. Admin manages."""

    __tablename__ = "system_settings"

    key: Mapped[str] = mapped_column(String(128), primary_key=True)
    value: Mapped[str] = mapped_column(Text, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, server_default=func.now(), onupdate=func.now()
    )
    updated_by_user_id: Mapped[int | None] = mapped_column(
        ForeignKey("users.id", ondelete="SET NULL"), nullable=True,
    )


class MigrationLog(Base):
    """Idempotency log: legacy row → new row mapping.

    Used by migrate_storage_configs to avoid creating duplicate
    StorageSource/DataSource rows when startup runs the migration twice.
    """

    __tablename__ = "migration_log"

    table_name: Mapped[str] = mapped_column(String(64), primary_key=True)
    legacy_id: Mapped[int] = mapped_column(Integer, primary_key=True)
    new_id: Mapped[int] = mapped_column(Integer, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())


class CheckTask(Base):
    __tablename__ = "check_tasks"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    # Legacy FKs — nullable since the v2 migration; one release of dual support.
    src_storage_id: Mapped[int | None] = mapped_column(
        ForeignKey("storage_configs.id"), nullable=True
    )
    src_path: Mapped[str] = mapped_column(String(512), nullable=False)
    dst_storage_id: Mapped[int | None] = mapped_column(
        ForeignKey("storage_configs.id"), nullable=True
    )
    # v2 FKs — nullable while both schemas coexist.
    src_data_source_id: Mapped[int | None] = mapped_column(
        ForeignKey("data_sources.id", ondelete="RESTRICT"), nullable=True, index=True,
    )
    dst_data_source_id: Mapped[int | None] = mapped_column(
        ForeignKey("data_sources.id", ondelete="RESTRICT"), nullable=True, index=True,
    )
    dst_path: Mapped[str] = mapped_column(String(512), nullable=False)
    cron: Mapped[str | None] = mapped_column(String(128), nullable=True)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    check_options: Mapped[dict] = mapped_column(JSON, nullable=False, default=dict)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, server_default=func.now(), onupdate=func.now()
    )

    src_storage: Mapped[StorageConfig | None] = relationship(foreign_keys=[src_storage_id])
    dst_storage: Mapped[StorageConfig | None] = relationship(foreign_keys=[dst_storage_id])
    src_data_source: Mapped["DataSource | None"] = relationship(foreign_keys=[src_data_source_id])
    dst_data_source: Mapped["DataSource | None"] = relationship(foreign_keys=[dst_data_source_id])
    runs: Mapped[list["CheckRun"]] = relationship(
        back_populates="task", cascade="all, delete-orphan"
    )


class SyncTask(Base):
    __tablename__ = "sync_tasks"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    # Legacy FKs — nullable since the v2 migration; one release of dual support.
    src_storage_id: Mapped[int | None] = mapped_column(
        ForeignKey("storage_configs.id"), nullable=True
    )
    src_path: Mapped[str] = mapped_column(String(512), nullable=False)
    dst_storage_id: Mapped[int | None] = mapped_column(
        ForeignKey("storage_configs.id"), nullable=True
    )
    # v2 FKs — nullable while both schemas coexist.
    src_data_source_id: Mapped[int | None] = mapped_column(
        ForeignKey("data_sources.id", ondelete="RESTRICT"), nullable=True, index=True,
    )
    dst_data_source_id: Mapped[int | None] = mapped_column(
        ForeignKey("data_sources.id", ondelete="RESTRICT"), nullable=True, index=True,
    )
    dst_path: Mapped[str] = mapped_column(String(512), nullable=False)
    mode: Mapped[SyncMode] = mapped_column(Enum(SyncMode), nullable=False)
    cron: Mapped[str] = mapped_column(String(128), nullable=False)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    rclone_options: Mapped[dict] = mapped_column(JSON, nullable=False, default=dict)
    pre_check_task_id: Mapped[int | None] = mapped_column(
        ForeignKey("check_tasks.id"), nullable=True
    )
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, server_default=func.now(), onupdate=func.now()
    )

    src_storage: Mapped[StorageConfig | None] = relationship(foreign_keys=[src_storage_id])
    dst_storage: Mapped[StorageConfig | None] = relationship(foreign_keys=[dst_storage_id])
    src_data_source: Mapped["DataSource | None"] = relationship(foreign_keys=[src_data_source_id])
    dst_data_source: Mapped["DataSource | None"] = relationship(foreign_keys=[dst_data_source_id])
    pre_check_task: Mapped[CheckTask | None] = relationship(
        foreign_keys=[pre_check_task_id]
    )
    runs: Mapped[list["SyncRun"]] = relationship(
        back_populates="task", cascade="all, delete-orphan"
    )


class SyncRun(Base):
    __tablename__ = "sync_runs"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    task_id: Mapped[int] = mapped_column(ForeignKey("sync_tasks.id"), nullable=False)
    job_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    status: Mapped[RunStatus] = mapped_column(
        Enum(RunStatus), nullable=False, default=RunStatus.pending
    )
    trigger: Mapped[RunTrigger] = mapped_column(Enum(RunTrigger), nullable=False)
    started_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    finished_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    stats: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    # Set when a failure webhook was sent; cleared on resolved. NULL = no
    # notification yet (or last one was a resolve). Used for dedup so the
    # poller doesn't fire duplicate alerts on re-entry.
    notified_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)

    task: Mapped[SyncTask] = relationship(back_populates="runs")


class CheckRun(Base):
    __tablename__ = "check_runs"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    task_id: Mapped[int] = mapped_column(ForeignKey("check_tasks.id"), nullable=False)
    job_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    status: Mapped[RunStatus] = mapped_column(
        Enum(RunStatus), nullable=False, default=RunStatus.pending
    )
    trigger: Mapped[RunTrigger] = mapped_column(Enum(RunTrigger), nullable=False)
    started_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    finished_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    result: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    notified_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)

    task: Mapped[CheckTask] = relationship(back_populates="runs")


# ---------------------------------------------------------------------------
# Cluster / leader election (active-standby HA)
# ---------------------------------------------------------------------------


class ClusterNode(Base):
    """One row per running process. Heartbeat drives liveness for /healthz."""

    __tablename__ = "cluster_nodes"

    node_id: Mapped[str] = mapped_column(String(64), primary_key=True)
    cluster_name: Mapped[str] = mapped_column(String(64), nullable=False, index=True)
    role: Mapped[str] = mapped_column(String(16), nullable=False)
    hostname: Mapped[str | None] = mapped_column(String(255))
    ip_address: Mapped[str | None] = mapped_column(String(45))
    started_at: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    last_heartbeat: Mapped[datetime] = mapped_column(DateTime, nullable=False)
    left_at: Mapped[datetime | None] = mapped_column(DateTime)


class LeaderLease(Base):
    """Single-row-per-cluster lease, contended via CAS UPDATE."""

    __tablename__ = "leader_lease"

    cluster_name: Mapped[str] = mapped_column(String(64), primary_key=True)
    leader_node_id: Mapped[str | None] = mapped_column(String(64))
    lease_until: Mapped[datetime | None] = mapped_column(DateTime)
    epoch: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)


# ---------------------------------------------------------------------------
# User accounts / RBAC
# ---------------------------------------------------------------------------


class UserRole(str, enum.Enum):
    admin = "admin"
    edit = "edit"
    view = "view"


class User(Base):
    """Application user; password is bcrypt-hashed in ``password_hash``."""

    __tablename__ = "users"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    username: Mapped[str] = mapped_column(String(64), unique=True, nullable=False)
    password_hash: Mapped[str] = mapped_column(String(255), nullable=False)
    role: Mapped[UserRole] = mapped_column(Enum(UserRole), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    last_login_at: Mapped[datetime | None] = mapped_column(DateTime)
    disabled_at: Mapped[datetime | None] = mapped_column(DateTime)
