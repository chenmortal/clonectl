import typer

from app.config import get_settings

cli = typer.Typer(help="rclone periodic sync service")


@cli.command("serve")
def serve() -> None:
    import logging

    import uvicorn

    settings = get_settings()
    # Resolve the level once and reuse for both root logger + uvicorn so the two
    # stay in sync. Unknown strings fall back to INFO instead of raising, so a
    # typo in .env never crashes the server.
    level = getattr(logging, settings.log_level.upper(), None)
    if not isinstance(level, int):
        level = logging.INFO
    logging.basicConfig(
        level=level,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    uvicorn.run(
        "app.main:app",
        host=settings.api_host,
        port=settings.api_port,
        log_level=settings.log_level.lower(),
    )


@cli.command("run-once")
def run_once(task_id: int) -> None:
    from app.db import session_factory
    from app.models import RunTrigger
    from app.rclone.client import RcloneClient
    from app.services.runner import run_task

    settings = get_settings()
    client = RcloneClient(settings.rclone_rc_url, settings.rclone_rc_user, settings.rclone_rc_pass)
    try:
        with session_factory() as session:
            run = run_task(session, client, task_id, RunTrigger.manual)
            typer.echo(f"run {run.id} status={run.status.value} job_id={run.job_id}")
    finally:
        client.close()


@cli.command("stop")
def stop() -> None:
    import os
    import signal
    import time
    from pathlib import Path

    settings = get_settings()

    def read_pid(path: Path) -> int | None:
        try:
            return int(path.read_text().strip())
        except (OSError, ValueError):
            return None

    def alive(pid: int) -> bool:
        try:
            os.kill(pid, 0)
            return True
        except ProcessLookupError:
            return False
        except PermissionError:
            return True

    def terminate(pid: int, timeout: float = 15.0) -> bool:
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            return True
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if not alive(pid):
                return True
            time.sleep(0.3)
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            return True
        return not alive(pid)

    main_pid_file = Path(settings.pid_file)
    main_pid = read_pid(main_pid_file)
    if main_pid is None:
        typer.echo("rclone-sync service: not running (no pid file)")
    elif not alive(main_pid):
        typer.echo("rclone-sync service: not running (stale pid file removed)")
        main_pid_file.unlink(missing_ok=True)
    else:
        typer.echo(f"stopping rclone-sync service (pid {main_pid})...")
        stopped = terminate(main_pid)
        main_pid_file.unlink(missing_ok=True)
        typer.echo(
            f"rclone-sync service: {'stopped' if stopped else 'failed to stop'}"
        )

    rcd_pid_file = Path("rcd.pid")
    rcd_pid = read_pid(rcd_pid_file)
    if rcd_pid is not None and alive(rcd_pid):
        typer.echo(f"stopping rclone rcd (pid {rcd_pid})...")
        stopped = terminate(rcd_pid)
        rcd_pid_file.unlink(missing_ok=True)
        typer.echo(f"rclone rcd: {'stopped' if stopped else 'failed to stop'}")
    else:
        rcd_pid_file.unlink(missing_ok=True)
        typer.echo("rclone rcd: not running")


@cli.command("status")
def status() -> None:
    from datetime import datetime, timedelta

    import httpx
    from sqlalchemy import select

    from app.db import session_factory
    from app.models import ClusterNode, LeaderLease
    from app.rclone.manager import RcloneManager

    settings = get_settings()
    manager = RcloneManager(
        settings.rclone_bin,
        settings.rclone_rc_addr,
        settings.rclone_rc_user,
        settings.rclone_rc_pass,
    )
    rclone_state = "running" if manager.is_running() else "stopped"
    typer.echo(f"rclone rcd: {rclone_state} ({settings.rclone_rc_url})")

    host = settings.api_host
    if host in ("0.0.0.0", "::"):
        host = "127.0.0.1"
    url = f"http://{host}:{settings.api_port}/healthz"
    cluster_section: dict[str, str] = {}
    try:
        resp = httpx.get(url, timeout=3.0)
        resp.raise_for_status()
        body = resp.json()
        reachable = body.get("rclone_reachable")
        cluster_section = {
            "leader_id": body.get("leader_id"),
            "node_id": body.get("node_id"),
            "peers": body.get("peers", []),
        }
        typer.echo(f"rclone-sync service: running ({url}, rclone_reachable={reachable})")
    except httpx.HTTPError:
        typer.echo(f"rclone-sync service: stopped ({url} unreachable)")

    # Cluster membership read straight from DB — works even when /healthz is down.
    typer.echo(f"cluster: name={settings.cluster_name}")
    try:
        cutoff = datetime.utcnow() - timedelta(
            seconds=max(
                2 * settings.heartbeat_interval_seconds,
                settings.lease_duration_seconds,
            )
        )
        with session_factory() as session:
            lease = session.get(LeaderLease, settings.cluster_name)
            nodes = session.scalars(
                select(ClusterNode).where(
                    ClusterNode.cluster_name == settings.cluster_name,
                    ClusterNode.left_at.is_(None),
                    ClusterNode.last_heartbeat >= cutoff,
                )
            ).all()
        leader = lease.leader_node_id if lease else None
        typer.echo(f"  leader:    {leader or '<none>'}")
        typer.echo(f"  nodes:     {[n.node_id for n in nodes]}")
        if cluster_section.get("node_id"):
            typer.echo(f"  local:     {cluster_section['node_id']}")
    except Exception as exc:  # noqa: BLE001
        typer.echo(f"  (failed to read cluster state: {exc})")


if __name__ == "__main__":
    cli()
