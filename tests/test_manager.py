from unittest.mock import patch

import pytest

from app.rclone.manager import RcloneManager


def test_start_command_includes_gui_and_metrics():
    manager = RcloneManager("rclone", "0.0.0.0:5572", "admin", "6051")

    with (
        patch.object(
            RcloneManager, "is_running", side_effect=[False, True]
        ) as is_running,
        patch("app.rclone.manager.subprocess.Popen") as popen,
        patch("app.rclone.manager.Path.open"),
        patch("app.rclone.manager.time.sleep"),
    ):
        manager.start()

    assert is_running.call_count == 2
    popen.return_value.poll.assert_not_called()
    cmd = popen.call_args.args[0]
    assert cmd[:2] == ["rclone", "rcd"]
    assert "--rc-addr=0.0.0.0:5572" in cmd
    assert "--rc-user=admin" in cmd
    assert "--rc-pass=6051" in cmd
    assert "--rc-serve" in cmd
    assert "--rc-web-gui" in cmd
    assert "--rc-enable-metrics" in cmd


def test_start_skipped_when_already_running():
    manager = RcloneManager("rclone", "0.0.0.0:5572", "admin", "6051")

    with (
        patch.object(RcloneManager, "is_running", return_value=True),
        patch("app.rclone.manager.subprocess.Popen") as popen,
    ):
        manager.start()

    popen.assert_not_called()


def test_start_raises_when_process_exits_early():
    manager = RcloneManager("rclone", "0.0.0.0:5572", "admin", "6051")

    with (
        patch.object(RcloneManager, "is_running", return_value=False),
        patch("app.rclone.manager.subprocess.Popen") as popen,
        patch("app.rclone.manager.Path.open"),
        patch("app.rclone.manager.time.sleep"),
    ):
        popen.return_value.poll.return_value = 1
        popen.return_value.returncode = 1
        with pytest.raises(RuntimeError, match="exited early"):
            manager.start()


def test_start_raises_on_readiness_timeout():
    manager = RcloneManager("rclone", "0.0.0.0:5572", "admin", "6051")

    with (
        patch.object(RcloneManager, "is_running", return_value=False),
        patch("app.rclone.manager.subprocess.Popen") as popen,
        patch("app.rclone.manager.Path.open"),
        patch("app.rclone.manager.time.sleep"),
        patch("app.rclone.manager.time.monotonic", side_effect=[0.0, 1.0, 20.0]),
    ):
        popen.return_value.poll.return_value = None
        with pytest.raises(RuntimeError, match="not ready"):
            manager.start()


def test_rc_url_maps_wildcard_to_localhost():
    manager = RcloneManager("rclone", "0.0.0.0:5572", "admin", "6051")
    assert manager._rc_url() == "http://127.0.0.1:5572"


def test_start_writes_pid_file(tmp_path):
    manager = RcloneManager(
        "rclone",
        "0.0.0.0:5572",
        "admin",
        "6051",
        log_path=str(tmp_path / "rcd.log"),
        pid_path=str(tmp_path / "rcd.pid"),
    )

    with (
        patch.object(RcloneManager, "is_running", side_effect=[False, True]),
        patch("app.rclone.manager.subprocess.Popen") as popen,
        patch("app.rclone.manager.time.sleep"),
    ):
        popen.return_value.pid = 12345
        popen.return_value.poll.return_value = None
        manager.start()

    assert (tmp_path / "rcd.pid").read_text() == "12345"


def test_stop_removes_pid_file(tmp_path):
    pid_file = tmp_path / "rcd.pid"
    pid_file.write_text("999")
    manager = RcloneManager(
        "rclone",
        "0.0.0.0:5572",
        "admin",
        "6051",
        log_path=str(tmp_path / "rcd.log"),
        pid_path=str(pid_file),
    )
    manager.stop()
    assert not pid_file.exists()
