from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    database_url: str = "sqlite:///./rclone_sync.db"
    rclone_rc_url: str = "http://localhost:5572"
    rclone_rc_user: str = "admin"
    rclone_rc_pass: str = "6051"
    rclone_managed: bool = True
    rclone_bin: str = "rclone"
    rclone_rc_addr: str = "0.0.0.0:5572"
    poll_interval_seconds: int = 10
    check_timeout_seconds: int = 3600
    api_host: str = "0.0.0.0"
    api_port: int = 8000
    pid_file: str = "rclone-sync.pid"

    # --- logging ---
    # Applied to the root logger (basicConfig) and to uvicorn. One of:
    # CRITICAL / ERROR / WARNING / INFO / DEBUG. Unknown values fall back to INFO
    # so a typo never silently swallows logs.
    log_level: str = "INFO"

    # --- active-standby HA ---
    # node_id default "" means LeaderElector will fall back to f"{hostname}-{pid}"
    node_id: str = ""
    cluster_name: str = "default"
    heartbeat_interval_seconds: int = 3
    lease_duration_seconds: int = 10
    peer_probe_enabled: bool = False
    peers: str = ""  # comma-separated http://host:port (reserved, unused in v1)

    # --- web static assets (built React bundle) ---
    # Path is resolved relative to the project root. If the directory does
    # not exist (e.g. tests, or before `npm run build`), FastAPI skips the
    # static mount and SPA fallback entirely — the API stays usable.
    static_dir: str = "web/dist"

    # --- auth / RBAC ---
    # JWT_SECRET MUST be replaced with 32+ random bytes in production. The default
    # value triggers a WARNING at startup but does not block it (so tests still work).
    jwt_secret: str = "CHANGE-ME-IN-PRODUCTION"
    jwt_algorithm: str = "HS256"
    jwt_expires_minutes: int = 60 * 8
    bcrypt_rounds: int = 12
    # When BOTH are set and the users table is empty, an admin account is created
    # at startup. Idempotent: re-running with users present is a no-op.
    bootstrap_admin_user: str = ""
    bootstrap_admin_password: str = ""

    # --- alerting ---
    # Default AlertManager webhook URL. May be overridden at runtime via
    # SystemSetting("alertmanager_url"). Empty string disables alerting.
    alertmanager_url: str = ""


@lru_cache
def get_settings() -> Settings:
    return Settings()
