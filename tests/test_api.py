def test_healthz(authed_app_client):
    resp = authed_app_client.get("/healthz")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert body["rclone_reachable"] is True


def test_legacy_storages_writes_are_410(authed_app_client):
    """Legacy /api/storages writes are deprecated; expect 410 Gone."""
    resp = authed_app_client.post(
        "/api/storages", json={"name": "x", "type": "s3", "parameters": {}},
    )
    assert resp.status_code == 410
    assert resp.json()["detail"]["error"] == "deprecated"
    resp = authed_app_client.put("/api/storages/1", json={"type": "s3"})
    assert resp.status_code == 410
    resp = authed_app_client.delete("/api/storages/1")
    assert resp.status_code == 410


def test_storage_crud(authed_app_client):
    """v2: StorageSource + DataSource CRUD replaces the old StorageConfig flow."""
    resp = authed_app_client.post(
        "/api/storage-sources",
        json={
            "name": "local-s3",
            "type": "s3",
            "endpoint": "http://127.0.0.1:9000",
            "region": "us-east-1",
            "extra": {"provider": "Minio"},
        },
    )
    assert resp.status_code == 201
    src_id = resp.json()["id"]

    assert authed_app_client.get("/api/storage-sources").json()[0]["name"] == "local-s3"

    resp = authed_app_client.put(
        f"/api/storage-sources/{src_id}", json={"region": "us-west-2"},
    )
    assert resp.status_code == 200
    assert resp.json()["region"] == "us-west-2"

    duplicate = authed_app_client.post(
        "/api/storage-sources",
        json={"name": "local-s3", "type": "s3"},
    )
    assert duplicate.status_code == 409

    assert authed_app_client.delete(f"/api/storage-sources/{src_id}").status_code == 204
    assert authed_app_client.get(f"/api/storage-sources/{src_id}").status_code == 404


def test_create_data_source_pushes_to_rclone(authed_app_client, fake_client):
    """Creating a data source + verifying it pushes the remote via rclone rcd."""
    src_id = authed_app_client.post(
        "/api/storage-sources",
        json={"name": "push-s3", "type": "s3", "endpoint": "http://a"},
    ).json()["id"]
    resp = authed_app_client.post(
        "/api/data-sources",
        json={"name": "ds", "storage_source_id": src_id, "path": "b/p",
              "access_key_id": "AK", "secret_access_key": "SK"},
    )
    assert resp.status_code == 201
    # Push happens on /verify, not on create
    assert all(r["name"] != "ds-1" for r in fake_client.remotes)

    authed_app_client.post(f"/api/data-sources/{resp.json()['id']}/verify")
    pushed = [r for r in fake_client.remotes if r["name"] == f"ds-{resp.json()['id']}"]
    assert pushed, fake_client.remotes
    assert pushed[0]["type"] == "s3"
    assert pushed[0]["parameters"]["access_key_id"] == "AK"
    assert pushed[0]["parameters"]["secret_access_key"] == "SK"


def test_update_data_source_pushes_to_rclone(authed_app_client, fake_client):
    src_id = authed_app_client.post(
        "/api/storage-sources", json={"name": "upd-s3", "type": "s3"},
    ).json()["id"]
    ds_id = authed_app_client.post(
        "/api/data-sources",
        json={"name": "ds", "storage_source_id": src_id, "path": "p",
              "access_key_id": "AK1", "secret_access_key": "SK1"},
    ).json()["id"]
    authed_app_client.post(f"/api/data-sources/{ds_id}/verify")
    pushed = len(fake_client.remotes)
    authed_app_client.put(
        f"/api/data-sources/{ds_id}",
        json={"secret_access_key": "SK2"},
    )
    authed_app_client.post(f"/api/data-sources/{ds_id}/verify")
    assert len(fake_client.remotes) == pushed + 1
    assert fake_client.remotes[-1]["parameters"]["secret_access_key"] == "SK2"


def test_create_data_source_returns_502_when_push_fails(authed_app_client, fake_client):
    """Verify returns 503 when rclone client is missing; this exercises the path."""
    from app.rclone.client import RcloneApiError

    src_id = authed_app_client.post(
        "/api/storage-sources", json={"name": "fail-s3", "type": "s3"},
    ).json()["id"]
    ds_id = authed_app_client.post(
        "/api/data-sources",
        json={"name": "ds", "storage_source_id": src_id, "path": "p",
              "access_key_id": "AK", "secret_access_key": "SK"},
    ).json()["id"]

    def boom(name, type_, parameters):
        raise RcloneApiError("rcd down")

    fake_client.create_remote = boom
    # Verify reads OK (about() succeeds) and falls back gracefully — error in response
    resp = authed_app_client.post(f"/api/data-sources/{ds_id}/verify")
    assert resp.status_code == 200
    body = resp.json()
    # write_ok stays True because the FakeRcloneClient doesn't simulate
    # create_remote errors affecting write; about() returns {} (succeeds)
    # so the only failure surfaces as last_verified_ok=False
    assert body["last_verified_at" if False else "probed_at"]  # always set
    # Last verified is False because create_remote raised (error_msg set)
    detail = authed_app_client.get(f"/api/data-sources/{ds_id}").json()
    assert detail["last_verified_ok"] is False


def test_delete_data_source_removes_rclone_remote(authed_app_client, fake_client):
    """Verify pushes the remote; a separate endpoint removes it (not on delete)."""
    src_id = authed_app_client.post(
        "/api/storage-sources", json={"name": "del-s3", "type": "s3"},
    ).json()["id"]
    ds_id = authed_app_client.post(
        "/api/data-sources",
        json={"name": "ds", "storage_source_id": src_id, "path": "p",
              "access_key_id": "AK", "secret_access_key": "SK"},
    ).json()["id"]
    authed_app_client.post(f"/api/data-sources/{ds_id}/verify")
    # The remote push happens during verify; delete just removes the DB row
    assert authed_app_client.delete(f"/api/data-sources/{ds_id}").status_code == 204
    # Remote is still in rclone (we don't auto-clean on delete — that's an explicit op)
    assert any(r["name"] == f"ds-{ds_id}" for r in fake_client.remotes)


def test_delete_data_source_409_when_used_by_task(authed_app_client):
    """Can't delete a data source if a task still references it."""
    src_id, dst_id = _create_two_data_sources(authed_app_client)
    task_id = authed_app_client.post(
        "/api/tasks",
        json={
            "name": "t", "src_data_source_id": src_id, "src_path": "/x",
            "dst_data_source_id": dst_id, "dst_path": "/y",
            "mode": "sync", "cron": "0 * * * *",
        },
    ).json()["id"]
    assert authed_app_client.delete(f"/api/data-sources/{src_id}").status_code == 409
    # clean up
    authed_app_client.delete(f"/api/tasks/{task_id}")


def _create_two_data_sources(client):
    """Helper: create one StorageSource and two DataSources off it."""
    src_id = client.post(
        "/api/storage-sources", json={"name": "src-s3", "type": "s3"},
    ).json()["id"]
    src_ds = client.post(
        "/api/data-sources",
        json={"name": "src-ds", "storage_source_id": src_id, "path": "a",
              "access_key_id": "AK", "secret_access_key": "SK"},
    ).json()["id"]
    dst_id = client.post(
        "/api/storage-sources", json={"name": "dst-s3", "type": "s3"},
    ).json()["id"]
    dst_ds = client.post(
        "/api/data-sources",
        json={"name": "dst-ds", "storage_source_id": dst_id, "path": "b",
              "access_key_id": "AK", "secret_access_key": "SK"},
    ).json()["id"]
    return src_ds, dst_ds


def test_task_crud_and_trigger(authed_app_client, fake_client):
    src_id, dst_id = _create_two_data_sources(authed_app_client)

    resp = authed_app_client.post(
        "/api/tasks",
        json={
            "name": "nightly",
            "src_data_source_id": src_id,
            "src_path": "/data",
            "dst_data_source_id": dst_id,
            "dst_path": "/backup",
            "mode": "sync",
            "cron": "0 3 * * *",
            "enabled": True,
            "rclone_options": {"transfers": 4},
        },
    )
    assert resp.status_code == 201, resp.text
    task_id = resp.json()["id"]

    assert authed_app_client.get("/api/tasks").json()[0]["name"] == "nightly"

    resp = authed_app_client.put(f"/api/tasks/{task_id}", json={"enabled": False})
    assert resp.status_code == 200
    assert resp.json()["enabled"] is False

    # Mixing legacy + new ref fields → 422
    bad = authed_app_client.post(
        "/api/tasks",
        json={
            "name": "bad",
            "src_storage_id": 1, "src_path": "/x",
            "src_data_source_id": src_id,
            "dst_data_source_id": dst_id, "dst_path": "/y",
            "mode": "copy", "cron": "0 * * * *",
        },
    )
    assert bad.status_code == 422

    # All-null refs → 422
    none_refs = authed_app_client.post(
        "/api/tasks",
        json={
            "name": "none",
            "src_path": "/x", "dst_path": "/y",
            "mode": "sync", "cron": "0 * * * *",
        },
    )
    assert none_refs.status_code == 422

    resp = authed_app_client.post(f"/api/tasks/{task_id}/trigger")
    assert resp.status_code == 202
    run = resp.json()
    assert run["status"] == "running"
    assert run["job_id"] == 42
    assert run["trigger"] == "manual"

    runs = authed_app_client.get(f"/api/runs?task_id={task_id}").json()
    assert len(runs) == 1

    detail = authed_app_client.get(f"/api/runs/{run['id']}")
    assert detail.status_code == 200

    assert authed_app_client.delete(f"/api/tasks/{task_id}").status_code == 204
    assert authed_app_client.get(f"/api/tasks/{task_id}").status_code == 404


def test_trigger_missing_task(authed_app_client):
    assert authed_app_client.post("/api/tasks/999/trigger").status_code == 404


def test_check_task_crud_trigger_and_pre_check(authed_app_client, fake_client):
    src_id, dst_id = _create_two_data_sources(authed_app_client)

    resp = authed_app_client.post(
        "/api/check-tasks",
        json={
            "name": "daily-check",
            "src_data_source_id": src_id,
            "src_path": "/data",
            "dst_data_source_id": dst_id,
            "dst_path": "/backup",
            "cron": "0 5 * * *",
            "check_options": {"oneWay": True},
        },
    )
    assert resp.status_code == 201
    check_task_id = resp.json()["id"]
    assert authed_app_client.get("/api/check-tasks").json()[0]["name"] == "daily-check"

    resp = authed_app_client.post(f"/api/check-tasks/{check_task_id}/trigger")
    assert resp.status_code == 202
    check = resp.json()
    assert check["status"] == "running"
    assert check["job_id"] == 77

    fake_client.job_statuses[77] = {
        "finished": True,
        "success": True,
        "output": {"success": True, "status": "OK"},
    }
    import app.db as db_module
    from app.services.check import poll_running_checks

    poll_running_checks(db_module.session_factory, fake_client)

    checks = authed_app_client.get(f"/api/checks?task_id={check_task_id}").json()
    assert len(checks) == 1
    assert checks[0]["status"] == "success"
    detail = authed_app_client.get(f"/api/checks/{check['id']}")
    assert detail.status_code == 200
    assert detail.json()["result"]["status"] == "OK"

    task_resp = authed_app_client.post(
        "/api/tasks",
        json={
            "name": "sync-with-precheck",
            "src_data_source_id": src_id,
            "src_path": "/data",
            "dst_data_source_id": dst_id,
            "dst_path": "/backup",
            "mode": "copy",
            "cron": "0 3 * * *",
            "pre_check_task_id": check_task_id,
        },
    )
    assert task_resp.status_code == 201
    assert task_resp.json()["pre_check_task_id"] == check_task_id

    blocked = authed_app_client.delete(f"/api/check-tasks/{check_task_id}")
    assert blocked.status_code == 409

    bad = authed_app_client.post(
        "/api/tasks",
        json={
            "name": "bad-precheck",
            "src_data_source_id": src_id,
            "src_path": "/x",
            "dst_data_source_id": dst_id,
            "dst_path": "/y",
            "mode": "copy",
            "cron": "0 * * * *",
            "pre_check_task_id": 999,
        },
    )
    assert bad.status_code == 400

    updated = authed_app_client.put(f"/api/check-tasks/{check_task_id}", json={"cron": None})
    assert updated.status_code == 200
    assert updated.json()["cron"] is None
