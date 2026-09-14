"""Tests for /api/system-settings."""
from __future__ import annotations

import pytest

from app.models import SystemSetting


def test_get_setting_404(authed_app_client):
    r = authed_app_client.get("/api/system-settings/missing")
    assert r.status_code == 404


def test_put_then_get_setting(authed_app_client, session):
    r = authed_app_client.put(
        "/api/system-settings/feature_x",
        json={"value": "hello"},
    )
    assert r.status_code == 200, r.text
    assert r.json()["value"] == "hello"

    r = authed_app_client.get("/api/system-settings/feature_x")
    assert r.status_code == 200
    assert r.json()["value"] == "hello"


def test_put_invalid_url_rejected(authed_app_client):
    r = authed_app_client.put(
        "/api/system-settings/alertmanager_url",
        json={"value": "not-a-url"},
    )
    assert r.status_code == 422


def test_put_alertmanager_url_accepts_empty_to_disable(authed_app_client):
    r = authed_app_client.put(
        "/api/system-settings/alertmanager_url",
        json={"value": ""},
    )
    assert r.status_code == 200
    assert r.json()["value"] == ""


def test_list_settings(authed_app_client):
    authed_app_client.put(
        "/api/system-settings/foo", json={"value": "1"},
    )
    authed_app_client.put(
        "/api/system-settings/bar", json={"value": "2"},
    )
    r = authed_app_client.get("/api/system-settings")
    assert r.status_code == 200
    keys = {row["key"] for row in r.json()}
    assert {"foo", "bar"}.issubset(keys)


def test_alertmanager_test_requires_url(authed_app_client):
    """No URL configured → 400."""
    r = authed_app_client.post(
        "/api/system-settings/internal/alertmanager-test",
        json={"alertname": "X"},
    )
    assert r.status_code == 400


def test_alertmanager_test_sends_webhook(authed_app_client, monkeypatch):
    """With a URL configured, the test endpoint POSTs a webhook."""
    captured = {}

    class FakeClient:
        def post(self, url, json):
            captured["url"] = url
            captured["payload"] = json
            import httpx
            return httpx.Response(200, json={})

    from app.services import alerts as alerts_module
    monkeypatch.setattr(alerts_module, "_client", FakeClient())

    authed_app_client.put(
        "/api/system-settings/alertmanager_url",
        json={"value": "http://alertmanager.local/webhook"},
    )
    r = authed_app_client.post(
        "/api/system-settings/internal/alertmanager-test",
        json={"alertname": "TestRun"},
    )
    assert r.status_code == 200, r.text
    assert r.json()["sent"] is True
    assert captured["url"] == "http://alertmanager.local/webhook"
    assert captured["payload"]["status"] == "firing"
    assert captured["payload"]["commonLabels"]["alertname"] == "TestRun"
