"""Tests for apprentice_orchestrator.quota."""

from __future__ import annotations

import json

from apprentice_orchestrator import quota


def test_default_quota(orch_env):
    cfg = orch_env
    q = quota.check_quota(cfg, "test-tenant")
    assert q["allowed"] is True
    assert q["max_loras"] == 4
    assert q["max_training_hours_monthly"] == 100
    assert q["training_hours_used"] == 0.0


def test_list_tenants_empty(orch_env):
    cfg = orch_env
    assert quota.list_tenants(cfg) == []


def test_list_tenants_after_create(orch_env):
    cfg = orch_env
    quota.get_quota(cfg, "tenant-a")
    quota.get_quota(cfg, "tenant-b")
    tenants = quota.list_tenants(cfg)
    assert tenants == ["tenant-a", "tenant-b"]


def test_get_quota(orch_env):
    cfg = orch_env
    q = quota.get_quota(cfg, "acme")
    assert q["tenant_id"] == "acme"
    assert q["max_loras"] == 4


def test_set_quota(orch_env):
    cfg = orch_env
    result = quota.set_quota(cfg, "acme", max_loras=8, max_vram_mb=32000)
    assert result["max_loras"] == 8
    assert result["max_vram_mb"] == 32000

    # Verify persisted.
    q = quota.get_quota(cfg, "acme")
    assert q["max_loras"] == 8
    assert q["max_vram_mb"] == 32000


def test_set_quota_rejects_unknown_keys(orch_env):
    cfg = orch_env
    result = quota.set_quota(cfg, "acme", max_loras=6, nonexistent=999)
    assert result["max_loras"] == 6


def test_check_quota_allowed(orch_env):
    cfg = orch_env
    q = quota.check_quota(cfg, "acme")
    assert q["allowed"] is True
    assert q["denied_reason"] is None


def test_check_quota_hours_exhausted(orch_env):
    cfg = orch_env
    quota.record_training_hours(cfg, "acme", 99.0)
    q = quota.check_quota(cfg, "acme")
    assert q["allowed"] is True  # 99 < 100

    quota.record_training_hours(cfg, "acme", 2.0)
    q = quota.check_quota(cfg, "acme")
    assert q["allowed"] is False
    assert "exhausted" in q["denied_reason"]


def test_record_training_hours_rounds(orch_env):
    cfg = orch_env
    quota.record_training_hours(cfg, "acme", 1.23456)
    q = quota.get_quota(cfg, "acme")
    assert q["training_hours_used"] == 1.235


def test_cli_quota_list(orch_env, capsys):
    from apprentice_orchestrator import cli
    quota.get_quota(orch_env, "tenant-x")
    cli.main(["quota", "list"])
    out = capsys.readouterr().out
    assert "tenant-x" in out


def test_cli_quota_get(orch_env, capsys):
    from apprentice_orchestrator import cli
    quota.set_quota(orch_env, "acme", max_loras=12)
    cli.main(["quota", "get", "--tenant-id", "acme"])
    out = capsys.readouterr().out
    data = json.loads(out)
    assert data["max_loras"] == 12


def test_cli_quota_set(orch_env, capsys):
    from apprentice_orchestrator import cli
    cli.main(["quota", "set", "--tenant-id", "acme", "--max-loras", "10"])
    out = capsys.readouterr().out
    data = json.loads(out)
    assert data["max_loras"] == 10
