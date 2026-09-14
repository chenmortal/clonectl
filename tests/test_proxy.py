import httpx


def test_proxy_forwards_path_body_and_query(authed_app_client, proxy_requests):
    resp = authed_app_client.post(
        "/rclone/sync/sync?foo=bar",
        json={"srcFs": "local-s3:/data", "dstFs": "remote-s3:/data", "_async": True},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["proxied"] is True
    assert body["path"] == "/sync/sync"
    assert resp.headers.get("x-rclone") == "rcd"

    upstream = proxy_requests[0]
    assert upstream.method == "POST"
    assert upstream.url.path == "/sync/sync"
    assert upstream.url.query == b"foo=bar"
    assert b"local-s3:/data" in upstream.content
    assert upstream.headers["authorization"].startswith("Basic ")


def test_proxy_root_and_get(authed_app_client, proxy_requests):
    resp = authed_app_client.get("/rclone/core/version")
    assert resp.status_code == 200
    assert proxy_requests[0].method == "GET"
    assert proxy_requests[0].url.path == "/core/version"


def test_proxy_upstream_error_returns_502(authed_app_client, proxy_requests):
    def failing(request):
        raise httpx.ConnectError("refused")

    authed_app_client.app.state.proxy_client = httpx.Client(
        base_url="http://rc.test", transport=httpx.MockTransport(failing)
    )
    resp = authed_app_client.post("/rclone/sync/sync", json={})
    assert resp.status_code == 502


def test_proxy_upstream_status_passthrough(authed_app_client):
    def handler(request):
        return httpx.Response(500, json={"error": "boom"})

    authed_app_client.app.state.proxy_client = httpx.Client(
        base_url="http://rc.test", transport=httpx.MockTransport(handler)
    )
    resp = authed_app_client.post("/rclone/job/status", json={"jobid": 1})
    assert resp.status_code == 500
    assert resp.json() == {"error": "boom"}
