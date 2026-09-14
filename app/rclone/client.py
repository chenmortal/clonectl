import os
import tempfile
import time
from typing import Any

import httpx


class RcloneApiError(Exception):
    def __init__(self, message: str, status_code: int | None = None, body: Any = None):
        super().__init__(message)
        self.status_code = status_code
        self.body = body


class RcloneClient:
    def __init__(
        self,
        base_url: str,
        username: str,
        password: str,
        timeout: float = 30.0,
        transport: httpx.BaseTransport | None = None,
    ):
        self._client = httpx.Client(
            base_url=base_url.rstrip("/"),
            auth=(username, password),
            timeout=timeout,
            transport=transport,
        )

    def close(self) -> None:
        self._client.close()

    def _post(
        self,
        path: str,
        payload: dict[str, Any] | None = None,
        allow_error_field: bool = False,
    ) -> dict[str, Any]:
        try:
            resp = self._client.post(path, json=payload or {})
        except httpx.HTTPError as exc:
            raise RcloneApiError(f"request {path} failed: {exc}") from exc
        return self._parse(path, resp, allow_error_field)

    def _parse(
        self, path: str, resp: httpx.Response, allow_error_field: bool = False
    ) -> dict[str, Any]:
        try:
            data = resp.json()
        except ValueError:
            data = {"raw": resp.text}
        if resp.status_code >= 400:
            raise RcloneApiError(
                f"rclone rc {path} returned {resp.status_code}", resp.status_code, data
            )
        if not allow_error_field and isinstance(data, dict) and data.get("error"):
            raise RcloneApiError(str(data["error"]), resp.status_code, data)
        return data if isinstance(data, dict) else {"result": data}

    def ping(self) -> bool:
        try:
            resp = self._client.post("/core/version", json={})
        except httpx.HTTPError:
            return False
        return resp.status_code == 200

    def create_remote(self, name: str, type_: str, parameters: dict[str, Any]) -> dict[str, Any]:
        return self._post(
            "/config/create",
            {"name": name, "type": type_, "parameters": parameters},
        )

    def delete_remote(self, name: str) -> dict[str, Any]:
        return self._post("/config/delete", {"name": name})

    def start_sync(
        self,
        src_fs: str,
        dst_fs: str,
        mode: str,
        options: dict[str, Any] | None = None,
    ) -> int:
        path = "/sync/sync" if mode == "sync" else "/sync/copy"
        payload: dict[str, Any] = {
            "srcFs": src_fs,
            "dstFs": dst_fs,
            "_async": True,
        }
        if options:
            payload["_config"] = options
        data = self._post(path, payload)
        jobid = data.get("jobid")
        if jobid is None:
            raise RcloneApiError(f"no jobid in response of {path}", body=data)
        return int(jobid)

    def job_status(self, job_id: int) -> dict[str, Any]:
        return self._post("/job/status", {"jobid": job_id}, allow_error_field=True)

    def start_check(
        self,
        src_fs: str,
        dst_fs: str,
        options: dict[str, Any] | None = None,
    ) -> int:
        payload: dict[str, Any] = {"srcFs": src_fs, "dstFs": dst_fs, "_async": True}
        if options:
            payload.update(options)
        data = self._post("/operations/check", payload)
        jobid = data.get("jobid")
        if jobid is None:
            raise RcloneApiError("no jobid in response of /operations/check", body=data)
        return int(jobid)

    def job_stats(self, job_id: int) -> dict[str, Any]:
        return self._post("/core/stats", {"group": f"job/{job_id}"})

    # --- verification probes (used by /api/data-sources/{id}/verify) ---

    def list(self, remote: str) -> list[dict[str, Any]]:
        """Probe read access on ``remote`` via ``operations/list``.

        `rclone rc operations/list` is the canonical read-probe endpoint:
        it lists objects at the path (empty for an empty bucket / prefix)
        and is exposed by every rclone rc daemon. Unlike ``about`` it does
        not require backend aggregation support — S3-style buckets that
        refuse ``about`` with 500 work fine here. Unlike the (CLI-only)
        ``lsd``, this method IS available through the rc interface.

        Raises :class:`RcloneApiError` on any rclone rc failure — the route
        handler maps that to ``read_ok=False``.
        """
        data = self._post("/operations/list", {"fs": remote, "remote": ""})
        # rclone rc returns either a list of entries or sometimes
        # ``{"list": [...]}`` — normalise.
        if isinstance(data, list):
            return data
        if isinstance(data, dict) and "list" in data:
            return data["list"]
        return []

    def write_probe(self, remote: str) -> tuple[bool, str | None]:
        """Verify write + delete permission on ``remote``.

        Writes a tiny probe file via ``operations/copyfile`` from a local
        temp file, then deletes it via ``operations/deletefile``. Returns
        ``(write_ok, error_or_cleanup_error)``:

        - ``(False, str)`` — upload failed; ``error_or_cleanup_error`` is
          the rclone error message. Caller should mark ``write_ok=False``.
        - ``(True, None)`` — write + cleanup both succeeded.
        - ``(True, str)`` — write succeeded but cleanup (delete) failed;
          the operator has write access but a stray file remains. Caller
          should mark ``write_ok=True`` and surface the cleanup warning.
        """
        name = f".rclone_sync_probe_{int(time.time() * 1000)}"
        fd, tmp = tempfile.mkstemp(prefix="rclone-sync-probe-")
        try:
            with os.fdopen(fd, "w") as f:
                f.write("rclone-sync verify probe\n")
            try:
                self._post(
                    "/operations/copyfile",
                    {
                        "srcFs": os.path.dirname(tmp),
                        "srcRemote": os.path.basename(tmp),
                        "dstFs": remote.rstrip("/"),
                        "dstRemote": name,
                    },
                )
            except RcloneApiError as exc:
                return False, str(exc)
            try:
                self._post(
                    "/operations/deletefile",
                    {"fs": remote.rstrip("/"), "remote": name},
                )
            except RcloneApiError as exc:
                return True, f"delete failed: {exc}"
            return True, None
        finally:
            try:
                os.unlink(tmp)
            except OSError:
                pass
