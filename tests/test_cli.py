from unittest.mock import patch

import httpx
from typer.testing import CliRunner

from cli import cli

runner = CliRunner()


def test_status_both_running():
    with (
        patch("app.rclone.manager.RcloneManager.is_running", return_value=True),
        patch("httpx.get") as get,
    ):
        get.return_value.json.return_value = {"status": "ok", "rclone_reachable": True}
        get.return_value.raise_for_status.return_value = None
        result = runner.invoke(cli, ["status"])

    assert result.exit_code == 0
    assert "rclone rcd: running" in result.output
    assert "rclone-sync service: running" in result.output
    assert "rclone_reachable=True" in result.output


def test_status_both_stopped():
    with (
        patch("app.rclone.manager.RcloneManager.is_running", return_value=False),
        patch("httpx.get", side_effect=httpx.ConnectError("refused")),
    ):
        result = runner.invoke(cli, ["status"])

    assert result.exit_code == 0
    assert "rclone rcd: stopped" in result.output
    assert "rclone-sync service: stopped" in result.output


def test_help_lists_commands():
    result = runner.invoke(cli, ["--help"])
    assert result.exit_code == 0
    assert "serve" in result.output
    assert "stop" in result.output
    assert "run-once" in result.output
    assert "status" in result.output
    assert "init-db" not in result.output
    assert "rcd" not in result.output


def test_stop_without_pid_files(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    result = runner.invoke(cli, ["stop"])
    assert result.exit_code == 0
    assert "rclone-sync service: not running" in result.output
    assert "rclone rcd: not running" in result.output


def test_stop_removes_stale_pid_file(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    pid_file = tmp_path / "rclone-sync.pid"
    pid_file.write_text("999999999")
    result = runner.invoke(cli, ["stop"])
    assert result.exit_code == 0
    assert "stale pid file removed" in result.output
    assert not pid_file.exists()
