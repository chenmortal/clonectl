import logging
import subprocess
import time
from pathlib import Path

import httpx

logger = logging.getLogger(__name__)


class RcloneManager:
    def __init__(
        self,
        bin: str,
        rc_addr: str,
        user: str,
        password: str,
        log_path: str = "rcd.log",
        pid_path: str = "rcd.pid",
    ):
        self.bin = bin
        self.rc_addr = rc_addr
        self.user = user
        self.password = password
        self.log_path = Path(log_path)
        self.pid_path = Path(pid_path)
        self._process: subprocess.Popen | None = None

    def _rc_url(self) -> str:
        host, _, port = self.rc_addr.rpartition(":")
        if host in ("0.0.0.0", "::"):
            host = "127.0.0.1"
        return f"http://{host}:{port}"

    def is_running(self) -> bool:
        try:
            resp = httpx.post(
                f"{self._rc_url()}/core/version",
                json={},
                auth=(self.user, self.password),
                timeout=3.0,
            )
            return resp.status_code == 200
        except httpx.HTTPError:
            return False

    def start(self, wait_timeout: float = 15.0) -> None:
        if self.is_running():
            logger.info("rclone rcd already running at %s", self.rc_addr)
            return
        cmd = [
            self.bin,
            "rcd",
            f"--rc-addr={self.rc_addr}",
            f"--rc-user={self.user}",
            f"--rc-pass={self.password}",
            "--rc-serve",
            "--rc-web-gui",
            "--rc-enable-metrics",
        ]
        log_file = self.log_path.open("a")
        self._process = subprocess.Popen(
            cmd, stdout=log_file, stderr=subprocess.STDOUT, start_new_session=True
        )
        self.pid_path.write_text(str(self._process.pid))
        deadline = time.monotonic() + wait_timeout
        while time.monotonic() < deadline:
            if self.is_running():
                logger.info("rclone rcd started at %s", self.rc_addr)
                return
            if self._process.poll() is not None:
                raise RuntimeError(
                    f"rclone rcd exited early (code {self._process.returncode}), "
                    f"see {self.log_path}"
                )
            time.sleep(0.5)
        raise RuntimeError(
            f"rclone rcd not ready within {wait_timeout}s, see {self.log_path}"
        )

    def stop(self) -> None:
        if self._process and self._process.poll() is None:
            self._process.terminate()
            try:
                self._process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self._process.kill()
        self._process = None
        self.pid_path.unlink(missing_ok=True)
