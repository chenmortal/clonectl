import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles

from app import db, models  # noqa: F401
from app.api import auth as auth_api
from app.api import check_tasks, checks, data_sources, proxy, runs, storage_sources, storages, system_settings, tasks, users
from app.auth.bootstrap import bootstrap_admin
from app.cluster.election import LeaderElector
from app.config import Settings, get_settings
from app.db import init_db
from app.rclone.client import RcloneClient
from app.rclone.manager import RcloneManager
from app.scheduler import SchedulerService
from app.schemas import HealthOut
from app.services.storage import sync_all_remotes

logger = logging.getLogger(__name__)


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or get_settings()

    # Silence APScheduler's per-job "Running job X ... executed successfully" spam
    # (heartbeat/elector and run/poll jobs all fire on short intervals).
    # Errors still propagate via the scheduler logger's normal handlers.
    logging.getLogger("apscheduler.executors.default").setLevel(logging.WARNING)
    logging.getLogger("apscheduler.scheduler").setLevel(logging.WARNING)

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        # JWT_SECRET validation lives here, NOT at module-import time, so that
        # `from app.main import app` / `uvicorn app.main:app` keeps working
        # even when the secret is misconfigured (uvicorn needs to import first
        # to emit the error itself). This runs on the first request, so a
        # misconfigured deployment fails fast instead of 500-ing logins.
        if not settings.jwt_secret:
            raise RuntimeError(
                "JWT_SECRET is empty. Set it to 32+ random bytes via env or .env. "
                "Generate one with: openssl rand -hex 32"
            )
        if settings.jwt_secret == "CHANGE-ME-IN-PRODUCTION":
            logger.warning(
                "JWT_SECRET is the built-in placeholder; set JWT_SECRET env var "
                "to 32+ random bytes for production deployments"
            )

        init_db()
        pid_file = Path(settings.pid_file)
        pid_file.write_text(str(os.getpid()))
        manager = None
        if settings.rclone_managed:
            manager = RcloneManager(
                settings.rclone_bin,
                settings.rclone_rc_addr,
                settings.rclone_rc_user,
                settings.rclone_rc_pass,
            )
            manager.start()
            app.state.rclone_manager = manager
        client = RcloneClient(
            settings.rclone_rc_url, settings.rclone_rc_user, settings.rclone_rc_pass
        )
        app.state.rclone_client = client
        proxy_client = httpx.Client(
            base_url=settings.rclone_rc_url,
            auth=(settings.rclone_rc_user, settings.rclone_rc_pass),
            timeout=60.0,
            follow_redirects=True,
        )
        app.state.proxy_client = proxy_client

        # v2: migrate legacy StorageConfig rows into StorageSource + DataSource
        # (idempotent) before pushing anything to rclone rcd. Seed the
        # AlertManager URL system setting from .env on first boot.
        from app.services.migrate_storage import migrate_storage_configs
        from app.models import SystemSetting

        try:
            migrate_counts = migrate_storage_configs(db.session_factory)
            logger.info("storage migration: %s", migrate_counts)
        except Exception:  # noqa: BLE001
            logger.exception("storage migration failed; service still starts")

        if settings.alertmanager_url:
            try:
                with db.session_factory() as s:
                    row = s.get(SystemSetting, "alertmanager_url")
                    if row is None:
                        s.add(SystemSetting(
                            key="alertmanager_url", value=settings.alertmanager_url,
                        ))
                        s.commit()
                        logger.info(
                            "seeded system_settings.alertmanager_url from .env"
                        )
            except Exception:  # noqa: BLE001
                logger.exception("failed to seed alertmanager_url system setting")

        try:
            synced, failed = sync_all_remotes(db.session_factory, client)
            logger.info(
                "startup: pushed %d storage config(s) to rclone rcd (%d failed)",
                synced,
                failed,
            )
        except Exception:  # noqa: BLE001
            logger.exception("startup: failed to push storage configs to rclone rcd")
        scheduler = SchedulerService(
            db.session_factory, client, settings.poll_interval_seconds
        )
        scheduler.start()
        app.state.scheduler = scheduler

        elector = LeaderElector(
            db.session_factory,
            node_id=settings.node_id,
            cluster_name=settings.cluster_name,
            heartbeat_interval_seconds=settings.heartbeat_interval_seconds,
            lease_duration_seconds=settings.lease_duration_seconds,
            on_acquired=lambda: scheduler.set_leader(True),
            on_lost=lambda: scheduler.set_leader(False),
        )
        try:
            elector.start()
            app.state.elector = elector
        except Exception:
            logger.exception("LeaderElector failed to start; running without HA")
            elector.stop()  # safe: nothing was started, but releases the BG scheduler
            app.state.elector = None

        try:
            bootstrap_admin(db.session_factory, settings)
        except Exception:
            logger.exception(
                "admin bootstrap failed; service still usable, log in as existing user"
            )

        yield

        if elector is not None:
            elector.stop()
        scheduler.shutdown()
        proxy_client.close()
        client.close()
        if manager is not None:
            manager.stop()
        pid_file.unlink(missing_ok=True)

    app = FastAPI(title="rclone-sync", lifespan=lifespan)
    app.include_router(auth_api.router)
    app.include_router(users.router)
    app.include_router(system_settings.router)
    app.include_router(storage_sources.router)
    app.include_router(data_sources.router)
    app.include_router(storages.router)
    app.include_router(tasks.router)
    app.include_router(check_tasks.router)
    app.include_router(runs.router)
    app.include_router(checks.router)
    app.include_router(proxy.router)

    @app.get("/healthz", response_model=HealthOut)
    def healthz():
        client = getattr(app.state, "rclone_client", None)
        reachable = bool(client.ping()) if client else False
        elector = getattr(app.state, "elector", None)
        if elector is not None:
            snap = elector.snapshot()
            return HealthOut(
                status="ok",
                rclone_reachable=reachable,
                is_leader=snap["is_leader"],
                node_id=snap["node_id"],
                cluster_name=snap["cluster_name"],
                leader_id=snap["leader_id"],
                peers=snap["peers"],
            )
        return HealthOut(status="ok", rclone_reachable=reachable)

    # ----- Web static assets (optional: only if `npm run build` was run) -----
    # Resolved relative to the project root, not the cwd, so tests and packaged
    # deployments behave the same. If the directory is missing we log info and
    # skip — the API stays fully usable (and so do /docs, /redoc, /openapi.json).
    static_root = Path(__file__).resolve().parent.parent / settings.static_dir
    if static_root.is_dir() and (static_root / "index.html").is_file():
        assets_dir = static_root / "assets"
        if assets_dir.is_dir():
            app.mount(
                "/assets",
                StaticFiles(directory=str(assets_dir)),
                name="web-assets",
            )
            logger.info("mounted static assets from %s", assets_dir)

        # SPA fallback: any GET not matched by API / docs routes → index.html.
        # Path("") represents the root; FastAPI's {full_path:path} matches everything.
        index_file = static_root / "index.html"

        @app.get("/", include_in_schema=False)
        async def spa_root() -> FileResponse:
            return FileResponse(index_file)

        @app.get("/{full_path:path}", include_in_schema=False)
        async def spa_fallback(full_path: str, request: Request) -> FileResponse:
            # If the path points at an actual file inside static_root (e.g. favicon.ico),
            # serve it; otherwise hand back index.html so the SPA router takes over.
            candidate = (static_root / full_path).resolve()
            try:
                candidate.relative_to(static_root)
            except ValueError:
                # attempt to escape the static root — refuse with index.html anyway
                return FileResponse(index_file)
            if candidate.is_file():
                return FileResponse(candidate)
            return FileResponse(index_file)
    else:
        logger.info(
            "static dir %s missing or has no index.html; "
            "run `npm --prefix web run build` to enable the web UI",
            static_root,
        )

    return app


app = create_app()
