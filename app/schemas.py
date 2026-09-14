from datetime import datetime
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

from app.models import BindingPermission, RunStatus, RunTrigger, SyncMode


class DataSourceCreate(BaseModel):
    """End-user data source.

    AK/SK are optional at the schema level — the route layer applies the
    type-aware rule: required unless the linked StorageSource.type is
    ``"local"`` (no credentials). See
    :func:`app.api.data_sources._enforce_credentials_for_remote_backends`.
    """

    name: str = Field(min_length=1, max_length=128)
    storage_source_id: int
    path: str = Field(min_length=1, max_length=512)
    access_key_id: str | None = Field(default=None, max_length=255)
    secret_access_key: str | None = Field(default=None, max_length=512)
    description: str | None = Field(default=None, max_length=512)


class DataSourceUpdate(BaseModel):
    name: str | None = Field(default=None, min_length=1, max_length=128)
    storage_source_id: int | None = None
    path: str | None = Field(default=None, min_length=1, max_length=512)
    access_key_id: str | None = Field(default=None, max_length=255)
    secret_access_key: str | None = Field(default=None, max_length=512)
    description: str | None = Field(default=None, max_length=512)


class StorageCreate(BaseModel):
    name: str = Field(min_length=1, max_length=128)
    type: str = Field(min_length=1, max_length=64)
    parameters: dict[str, Any] = Field(default_factory=dict)


class StorageUpdate(BaseModel):
    name: str | None = None
    type: str | None = None
    parameters: dict[str, Any] | None = None


class StorageOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    type: str
    parameters: dict[str, Any]
    created_at: datetime
    updated_at: datetime


class TaskCreate(BaseModel):
    name: str = Field(min_length=1, max_length=128)
    src_storage_id: int
    src_path: str
    dst_storage_id: int
    dst_path: str
    mode: SyncMode
    cron: str
    enabled: bool = True
    rclone_options: dict[str, Any] = Field(default_factory=dict)
    pre_check_task_id: int | None = None


class TaskUpdate(BaseModel):
    name: str | None = None
    src_storage_id: int | None = None
    src_path: str | None = None
    dst_storage_id: int | None = None
    dst_path: str | None = None
    mode: SyncMode | None = None
    cron: str | None = None
    enabled: bool | None = None
    rclone_options: dict[str, Any] | None = None
    pre_check_task_id: int | None = None


class TaskOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    src_storage_id: int
    src_path: str
    dst_storage_id: int
    dst_path: str
    mode: SyncMode
    cron: str
    enabled: bool
    rclone_options: dict[str, Any]
    pre_check_task_id: int | None
    created_at: datetime
    updated_at: datetime


class CheckTaskCreate(BaseModel):
    name: str = Field(min_length=1, max_length=128)
    src_storage_id: int
    src_path: str
    dst_storage_id: int
    dst_path: str
    cron: str | None = None
    enabled: bool = True
    check_options: dict[str, Any] = Field(default_factory=dict)


class CheckTaskUpdate(BaseModel):
    name: str | None = None
    src_storage_id: int | None = None
    src_path: str | None = None
    dst_storage_id: int | None = None
    dst_path: str | None = None
    cron: str | None = None
    enabled: bool | None = None
    check_options: dict[str, Any] | None = None


class CheckTaskOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    src_storage_id: int
    src_path: str
    dst_storage_id: int
    dst_path: str
    cron: str | None
    enabled: bool
    check_options: dict[str, Any]
    created_at: datetime
    updated_at: datetime


class RunOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    task_id: int
    job_id: int | None
    status: RunStatus
    trigger: RunTrigger
    started_at: datetime | None
    finished_at: datetime | None
    error: str | None
    stats: dict[str, Any] | None


class RunDetail(RunOut):
    live: dict[str, Any] | None = None


class CheckOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    task_id: int
    job_id: int | None
    status: RunStatus
    trigger: RunTrigger
    started_at: datetime | None
    finished_at: datetime | None
    error: str | None
    result: dict[str, Any] | None


class CheckDetail(CheckOut):
    live: dict[str, Any] | None = None


class HealthOut(BaseModel):
    status: str
    rclone_reachable: bool
    is_leader: bool = False
    node_id: str = ""
    cluster_name: str = ""
    leader_id: str | None = None
    peers: list[str] = Field(default_factory=list)


# ---------------------------------------------------------------------------
# Auth / user management
# ---------------------------------------------------------------------------


class LoginIn(BaseModel):
    username: str
    password: str


class TokenOut(BaseModel):
    access_token: str
    token_type: str = "bearer"
    expires_in: int
    role: str


class MeOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    username: str
    role: str
    last_login_at: datetime | None


class ChangePasswordIn(BaseModel):
    old_password: str
    new_password: str = Field(min_length=8, max_length=128)


class UserCreate(BaseModel):
    username: str = Field(min_length=1, max_length=64)
    password: str = Field(min_length=8, max_length=128)
    role: str  # validated against UserRole at the route layer


class UserUpdate(BaseModel):
    role: str | None = None
    disabled: bool | None = None


class UserOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    username: str
    role: str
    created_at: datetime
    last_login_at: datetime | None
    disabled_at: datetime | None


class ResetPasswordIn(BaseModel):
    new_password: str = Field(min_length=8, max_length=128)


# ---------------------------------------------------------------------------
# StorageSource (admin-managed provider template)
# ---------------------------------------------------------------------------


class StorageSourceCreate(BaseModel):
    name: str = Field(min_length=1, max_length=128, pattern=r"^[a-zA-Z0-9_-]+$")
    type: str = Field(min_length=1, max_length=64)
    endpoint: str | None = Field(default=None, max_length=512)
    region: str | None = Field(default=None, max_length=64)
    extra: dict[str, Any] = Field(default_factory=dict)


class StorageSourceUpdate(BaseModel):
    name: str | None = Field(default=None, min_length=1, max_length=128, pattern=r"^[a-zA-Z0-9_-]+$")
    type: str | None = Field(default=None, min_length=1, max_length=64)
    endpoint: str | None = Field(default=None, max_length=512)
    region: str | None = Field(default=None, max_length=64)
    extra: dict[str, Any] | None = None


class StorageSourceOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    type: str
    endpoint: str | None
    region: str | None
    extra: dict[str, Any]
    created_at: datetime
    updated_at: datetime


# ---------------------------------------------------------------------------
# DataSource (end-user "bucket + path" bound to a StorageSource)
# ---------------------------------------------------------------------------


class DataSourceOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    storage_source_id: int
    path: str
    # Nullable since v3 — local-FS sources don't carry credentials.
    access_key_id: str | None
    secret_access_key: str | None
    description: str | None
    owner_user_id: int
    last_verified_at: datetime | None
    last_verified_ok: bool | None
    created_at: datetime
    updated_at: datetime


class DataSourceVerifyOut(BaseModel):
    read_ok: bool
    write_ok: bool
    error: str | None = None
    probed_at: datetime


# ---------------------------------------------------------------------------
# DataSourceBinding (M:N user ↔ DataSource)
# ---------------------------------------------------------------------------


DataSourcePermission = Literal["read", "write", "admin"]


class DataSourceBindingCreate(BaseModel):
    user_id: int
    permission: DataSourcePermission


class DataSourceBindingUpdate(BaseModel):
    permission: DataSourcePermission


class DataSourceBindingOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    data_source_id: int
    user_id: int
    permission: BindingPermission
    created_at: datetime
    created_by_user_id: int | None


# ---------------------------------------------------------------------------
# SystemSetting
# ---------------------------------------------------------------------------


class SystemSettingUpdate(BaseModel):
    value: str = Field(min_length=0, max_length=4096)


class SystemSettingOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    key: str
    value: str
    updated_at: datetime
    updated_by_user_id: int | None


class AlertmanagerTestIn(BaseModel):
    alertname: str = "ManualTest"
    labels: dict[str, str] = Field(default_factory=dict)


class AlertmanagerTestOut(BaseModel):
    sent: bool
    status_code: int
    error: str | None = None


# ---------------------------------------------------------------------------
# Task payloads — extended to support data_source_id alongside legacy storage_id
# ---------------------------------------------------------------------------


class _TaskCreateMixin(BaseModel):
    """Common validator: task refs must use exactly one of the storage/data_source
    styles — both new fields set, or both legacy fields set. Any partial
    combination is rejected."""

    @model_validator(mode="after")
    def _validate_refs(self) -> "_TaskCreateMixin":
        # Per-side: each of src/dst must be set via exactly one of the two
        # available refs (storage_id or data_source_id). Mixing sides is
        # allowed (e.g. legacy src + new dst) — but on a given side the two
        # must agree.
        for side in ("src", "dst"):
            storage_id = getattr(self, f"{side}_storage_id", None)
            ds_id = getattr(self, f"{side}_data_source_id", None)
            if storage_id is not None and ds_id is not None:
                raise ValueError(
                    f"{side}: set either storage_id or data_source_id, not both"
                )
        new_set = (
            self.src_data_source_id is not None
            and self.dst_data_source_id is not None
        )
        legacy_set = (
            getattr(self, "src_storage_id", None) is not None
            and getattr(self, "dst_storage_id", None) is not None
        )
        if not new_set and not legacy_set:
            raise ValueError(
                "must set both src_data_source_id and dst_data_source_id "
                "(new) or both src_storage_id and dst_storage_id (legacy)"
            )
        if new_set and legacy_set:
            raise ValueError(
                "set either the new data_source_id pair or the legacy "
                "storage_id pair, not both"
            )
        return self


class TaskCreate(_TaskCreateMixin, BaseModel):
    name: str = Field(min_length=1, max_length=128)
    # New (preferred) fields
    src_data_source_id: int | None = None
    dst_data_source_id: int | None = None
    # Legacy (deprecated) fields — accepted during dual-write window
    src_storage_id: int | None = None
    dst_storage_id: int | None = None
    # Path is shared
    src_path: str
    dst_path: str
    mode: SyncMode
    cron: str
    enabled: bool = True
    rclone_options: dict[str, Any] = Field(default_factory=dict)
    pre_check_task_id: int | None = None


class TaskUpdate(BaseModel):
    name: str | None = None
    src_data_source_id: int | None = None
    dst_data_source_id: int | None = None
    src_storage_id: int | None = None
    dst_storage_id: int | None = None
    src_path: str | None = None
    dst_path: str | None = None
    mode: SyncMode | None = None
    cron: str | None = None
    enabled: bool | None = None
    rclone_options: dict[str, Any] | None = None
    pre_check_task_id: int | None = None


class TaskOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    src_data_source_id: int | None
    dst_data_source_id: int | None
    src_storage_id: int | None
    dst_storage_id: int | None
    src_path: str
    dst_path: str
    mode: SyncMode
    cron: str
    enabled: bool
    rclone_options: dict[str, Any]
    pre_check_task_id: int | None
    created_at: datetime
    updated_at: datetime


class CheckTaskCreate(_TaskCreateMixin, BaseModel):
    name: str = Field(min_length=1, max_length=128)
    src_data_source_id: int | None = None
    dst_data_source_id: int | None = None
    src_storage_id: int | None = None
    dst_storage_id: int | None = None
    src_path: str
    dst_path: str
    cron: str | None = None
    enabled: bool = True
    check_options: dict[str, Any] = Field(default_factory=dict)


class CheckTaskUpdate(BaseModel):
    name: str | None = None
    src_data_source_id: int | None = None
    dst_data_source_id: int | None = None
    src_storage_id: int | None = None
    dst_storage_id: int | None = None
    src_path: str | None = None
    dst_path: str | None = None
    cron: str | None = None
    enabled: bool | None = None
    check_options: dict[str, Any] | None = None


class CheckTaskOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    name: str
    src_data_source_id: int | None
    dst_data_source_id: int | None
    src_storage_id: int | None
    dst_storage_id: int | None
    src_path: str
    dst_path: str
    cron: str | None
    enabled: bool
    check_options: dict[str, Any]
    created_at: datetime
    updated_at: datetime
