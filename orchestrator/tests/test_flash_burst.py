"""Tests for apprentice_orchestrator.flash_burst — RunPod burst dispatcher."""

from __future__ import annotations

import json
from unittest.mock import patch

import pytest

from apprentice_orchestrator import flash_burst
from apprentice_orchestrator.config import Config


def _mock_budget_passed(cfg, tenant_id):
    return {"allowed": True, "spent": 5.0, "budget": 20.0, "pct": 25, "threshold": "ok"}


def _mock_budget_blocked(cfg, tenant_id):
    return {"allowed": False, "spent": 20.0, "budget": 20.0, "pct": 100, "threshold": "exhausted"}


def _mock_quota_passed(cfg, tenant_id):
    return {"allowed": True}


def _mock_quota_blocked(cfg, tenant_id):
    return {"allowed": False, "denied_reason": "training hours exhausted"}


@pytest.fixture
def cfg(tmp_path, monkeypatch):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    return Config()


def test_list_gpu_types():
    gpus = flash_burst.list_gpu_types()
    assert len(gpus) == 3
    names = {g["name"] for g in gpus}
    assert names == {"A100", "A6000", "L40S"}
    for g in gpus:
        assert "cost_per_sec" in g
        assert "cost_per_hr" in g
        assert "vram_mb" in g


def test_can_burst_allowed(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_passed)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)
    monkeypatch.setenv("RUNPOD_API_KEY", "test-key")

    decision = flash_burst.can_burst(cfg, "t1", gpu="A6000")
    assert decision["allowed"] is True
    assert decision["tenant_id"] == "t1"
    assert decision["gpu"] == "A6000"
    assert decision["reasons"] == []


def test_can_burst_blocked_by_budget(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_blocked)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)

    decision = flash_burst.can_burst(cfg, "t1")
    assert decision["allowed"] is False
    assert any("budget" in r for r in decision["reasons"])


def test_can_burst_blocked_by_quota(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_passed)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_blocked)

    decision = flash_burst.can_burst(cfg, "t1")
    assert decision["allowed"] is False
    assert any("quota" in r for r in decision["reasons"])


def test_can_burst_blocked_no_api_key(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_passed)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)
    monkeypatch.setenv("RUNPOD_API_KEY", "")

    decision = flash_burst.can_burst(cfg, "t1")
    assert decision["allowed"] is False
    assert any("RUNPOD_API_KEY" in r for r in decision["reasons"])


def test_can_burst_blocked_unknown_gpu(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_passed)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)
    monkeypatch.setenv("RUNPOD_API_KEY", "fake-key")

    decision = flash_burst.can_burst(cfg, "t1", gpu="RTX5090")
    assert decision["allowed"] is False
    assert any("unknown GPU" in r for r in decision["reasons"])


def test_provision_pod_blocked_by_budget(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_blocked)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)

    result = flash_burst.provision_pod(cfg, "t1")
    assert "error" in result
    assert result["error"] == "burst blocked"


def test_provision_pod_missing_api_key(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_passed)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)
    monkeypatch.delenv("RUNPOD_API_KEY", raising=False)

    result = flash_burst.provision_pod(cfg, "t1")
    assert result["error"] == "burst blocked"
    assert any("RUNPOD_API_KEY" in r for r in result.get("reasons", []))


def _fake_graphql(query, api_key, variables=None):
    if "podFindAndDeployOnDemand" in query:
        return {
            "data": {
                "podFindAndDeployOnDemand": {
                    "id": "pod-abc123",
                    "name": "apprentice-burst-t1",
                    "machineId": "m1",
                    "gpuCount": 1,
                    "costPerHr": 2.736,
                    "desiredStatus": "RUNNING",
                    "imageName": "runpod/pytorch:2.1.0",
                }
            }
        }
    if "podTerminate" in query:
        return {"data": {"podTerminate": "ok"}}
    if "myself" in query:
        return {
            "data": {
                "myself": {
                    "pods": [
                        {
                            "id": "pod-1",
                            "name": "burst-pod",
                            "costPerHr": 2.736,
                            "desiredStatus": "RUNNING",
                            "runtime": {
                                "uptimeInSeconds": 3600,
                                "gpus": [{"gpuUtilPercent": 85, "memoryUtilPercent": 60}],
                            },
                        }
                    ]
                }
            }
        }
    return {}


@pytest.fixture
def mock_runpod(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "check_budget", _mock_budget_passed)
    monkeypatch.setattr(flash_burst.quota_mod, "check_quota", _mock_quota_passed)
    monkeypatch.setattr(flash_burst.budget_mod, "record", lambda *a, **kw: None)
    monkeypatch.setattr(flash_burst.quota_mod, "record_training_hours", lambda *a: None)
    monkeypatch.setenv("RUNPOD_API_KEY", "test-key")
    monkeypatch.setattr(flash_burst, "_graphql", _fake_graphql)


def test_provision_pod_success(cfg, mock_runpod):
    result = flash_burst.provision_pod(cfg, "t1")
    assert result["pod_id"] == "pod-abc123"
    assert result["gpu_type"] == "NVIDIA A100 80GB PCIe"
    assert result["cost_per_hr"] == 2.736
    assert result["status"] == "RUNNING"


def test_provision_pod_custom_gpu(cfg, mock_runpod):
    result = flash_burst.provision_pod(cfg, "t1", gpu="L40S", gpu_count=1)
    assert result["pod_id"] == "pod-abc123"


def test_terminate_pod_success(cfg, mock_runpod):
    result = flash_burst.terminate_pod(cfg, "t1", "pod-1")
    assert result["terminated"] is True
    assert result["pod_id"] == "pod-1"


def test_terminate_pod_missing_api_key(cfg, monkeypatch):
    monkeypatch.delenv("RUNPOD_API_KEY", raising=False)
    result = flash_burst.terminate_pod(cfg, "t1", "pod-1")
    assert result == {"error": "RUNPOD_API_KEY not set"}


def test_list_pods_success(cfg, mock_runpod):
    pods = flash_burst.list_pods(cfg, "t1")
    assert len(pods) == 1
    assert pods[0]["pod_id"] == "pod-1"
    assert pods[0]["name"] == "burst-pod"
    assert pods[0]["cost_per_hr"] == 2.736
    assert pods[0]["uptime_hours"] == 1.0
    assert pods[0]["accrued_cost"] == 2.736


def test_list_pods_missing_api_key(cfg, monkeypatch):
    monkeypatch.delenv("RUNPOD_API_KEY", raising=False)
    result = flash_burst.list_pods(cfg, "t1")
    assert result == []


def test_record_run(cfg, monkeypatch):
    budget_calls = []
    quota_calls = []

    monkeypatch.setattr(
        flash_burst.budget_mod, "record",
        lambda c, t, cost, source: budget_calls.append((t, cost, source)),
    )
    monkeypatch.setattr(
        flash_burst.quota_mod, "record_training_hours",
        lambda c, t, hrs: quota_calls.append((t, hrs)),
    )

    result = flash_burst.record_run(cfg, "t1", "p1", 3600.0, gpu="A100")
    an_hour_cost = 3600 * 0.00076

    assert result["tenant_id"] == "t1"
    assert result["pattern_id"] == "p1"
    assert result["gpu"] == "A100"
    assert result["runtime_seconds"] == 3600.0
    assert abs(result["cost_usd"] - an_hour_cost) < 0.001
    assert abs(result["training_hours"] - 1.0) < 0.001
    assert len(budget_calls) == 1
    assert budget_calls[0][0] == "t1"
    assert abs(budget_calls[0][1] - an_hour_cost) < 0.001
    assert len(quota_calls) == 1


def test_record_run_with_different_gpu(cfg, monkeypatch):
    monkeypatch.setattr(flash_burst.budget_mod, "record", lambda *a, **kw: None)
    monkeypatch.setattr(flash_burst.quota_mod, "record_training_hours", lambda *a: None)

    result = flash_burst.record_run(cfg, "t1", "p1", 7200.0, gpu="L40S")
    expected = 7200 * 0.00032
    assert abs(result["cost_usd"] - expected) < 0.001
    assert abs(result["training_hours"] - 2.0) < 0.001
