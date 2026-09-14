import json

import httpx
import pytest

from app.rclone.client import RcloneApiError, RcloneClient


def make_client(handler):
    return RcloneClient("http://rc.test", "admin", "pass", transport=httpx.MockTransport(handler))


def test_start_sync_returns_jobid():
    captured = {}

    def handler(request):
        captured["path"] = request.url.path
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={"jobid": 7})

    client = make_client(handler)
    jobid = client.start_sync("local-s3:/data", "remote-s3:/data", "sync", {"transfers": 4})
    assert jobid == 7
    assert captured["path"] == "/sync/sync"
    assert captured["body"]["srcFs"] == "local-s3:/data"
    assert captured["body"]["dstFs"] == "remote-s3:/data"
    assert captured["body"]["_async"] is True
    assert captured["body"]["_config"] == {"transfers": 4}


def test_start_copy_mode_path():
    def handler(request):
        assert request.url.path == "/sync/copy"
        return httpx.Response(200, json={"jobid": 3})

    client = make_client(handler)
    assert client.start_sync("a:/x", "b:/y", "copy") == 3


def test_http_error_raises():
    def handler(request):
        return httpx.Response(500, json={"error": "internal error"})

    client = make_client(handler)
    with pytest.raises(RcloneApiError) as excinfo:
        client.start_sync("a:/x", "b:/y", "sync")
    assert excinfo.value.status_code == 500


def test_create_remote_payload():
    captured = {}

    def handler(request):
        captured["path"] = request.url.path
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={})

    client = make_client(handler)
    client.create_remote("local-s3", "s3", {"provider": "Minio"})
    assert captured["path"] == "/config/create"
    assert captured["body"] == {
        "name": "local-s3",
        "type": "s3",
        "parameters": {"provider": "Minio"},
    }


def test_delete_remote_payload():
    captured = {}

    def handler(request):
        captured["path"] = request.url.path
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={})

    client = make_client(handler)
    client.delete_remote("local-s3")
    assert captured["path"] == "/config/delete"
    assert captured["body"] == {"name": "local-s3"}


def test_job_status_failed_job_returns_body_without_raising():
    def handler(request):
        return httpx.Response(
            200,
            json={"finished": True, "success": False, "error": "directory not found", "id": 9},
        )

    client = make_client(handler)
    data = client.job_status(9)
    assert data["finished"] is True
    assert data["success"] is False
    assert data["error"] == "directory not found"


def test_job_status_and_stats():
    def handler(request):
        body = json.loads(request.content)
        if request.url.path == "/job/status":
            assert body == {"jobid": 9}
            return httpx.Response(200, json={"finished": True, "success": True})
        if request.url.path == "/core/stats":
            assert body == {"group": "job/9"}
            return httpx.Response(200, json={"bytes": 10})
        raise AssertionError(request.url.path)

    client = make_client(handler)
    assert client.job_status(9)["finished"] is True
    assert client.job_stats(9)["bytes"] == 10


def test_ping_true_and_false():
    client = make_client(lambda request: httpx.Response(200, json={"version": "1.69"}))
    assert client.ping() is True

    def failing(request):
        raise httpx.ConnectError("refused")

    client = make_client(failing)
    assert client.ping() is False
