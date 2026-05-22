"""RunPod burst dispatcher (Phase 2g).

Option A — Flash (primary): Launch serverless GPU endpoints via RunPod Flash API.
Option B — Pods (fallback): Provision persistent pods via RunPod GraphQL.

Every cloud provisioning call is gated through ``budget.check_budget()``.
After a run completes, spend and training hours are recorded.

Usage::

    from apprentice_orchestrator import flash_burst

    # Check if burst is possible before training.
    decision = flash_burst.can_burst(cfg, "acme-corp")
    if not decision["allowed"]:
        print("Blocked:", decision["reason"])

    # Launch a training run on a RunPod Flash endpoint.
    run = flash_burst.launch_flash(cfg, "acme-corp", pattern_id, dataset_dir)
"""

from __future__ import annotations

import json
import logging
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

from . import budget as budget_mod
from . import quota as quota_mod
from .config import Config

LOG = logging.getLogger("apprentice_orchestrator.flash_burst")

RUNPOD_GRAPHQL_URL = "https://api.runpod.io/graphql"
RUNPOD_REST_URL = "https://api.runpod.io/v2"
RUNPOD_API_URL = "https://rest.runpod.io/v1"

FLASH_GPU_TYPES: dict[str, dict[str, Any]] = {
    "A100": {
        "gpu_type": "NVIDIA A100 80GB PCIe",
        "gpu_count": 1,
        "cost_per_sec": 0.00076,
        "cost_per_hr": 2.736,
        "vram_mb": 80000,
        "min_vram_gb": 40,
    },
    "A6000": {
        "gpu_type": "NVIDIA RTX A6000",
        "gpu_count": 1,
        "cost_per_sec": 0.00034,
        "cost_per_hr": 1.224,
        "vram_mb": 48000,
        "min_vram_gb": 24,
    },
    "L40S": {
        "gpu_type": "NVIDIA L40S",
        "gpu_count": 1,
        "cost_per_sec": 0.00032,
        "cost_per_hr": 1.152,
        "vram_mb": 48000,
        "min_vram_gb": 24,
    },
}


def _runpod_api_key(cfg: Config) -> str | None:
    import os

    return os.environ.get("RUNPOD_API_KEY") or None


def _graphql(query: str, api_key: str, variables: dict | None = None) -> dict[str, Any]:
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    req = urllib.request.Request(RUNPOD_GRAPHQL_URL, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", f"Bearer {api_key}")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"RunPod GraphQL HTTP {e.code}: {e.read().decode(errors='replace')[:500]}")
    except urllib.error.URLError as e:
        raise RuntimeError(f"RunPod GraphQL connection error: {e.reason}")
    if "errors" in data and data["errors"]:
        msgs = "; ".join(e.get("message", str(e)) for e in data["errors"])
        raise RuntimeError(f"RunPod GraphQL errors: {msgs}")
    return data


def _rest_get(path: str, api_key: str) -> dict[str, Any]:
    req = urllib.request.Request(f"{RUNPOD_API_URL}{path}")
    req.add_header("Authorization", f"Bearer {api_key}")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"RunPod REST HTTP {e.code}: {e.read().decode(errors='replace')[:500]}")
    except urllib.error.URLError as e:
        raise RuntimeError(f"RunPod REST connection error: {e.reason}")


def can_burst(cfg: Config, tenant_id: str, gpu: str = "A100") -> dict[str, Any]:
    """Check if cloud burst is allowed for *tenant_id*.

    Verifies budget, quota, and RunPod API key availability.
    Returns a decision dict with ``allowed`` and ``reason``.
    """
    reasons: list[str] = []

    budget = budget_mod.check_budget(cfg, tenant_id)
    if not budget["allowed"]:
        reasons.append(f"budget {budget['threshold']}: spent ${budget['spent']} of ${budget['budget']} ({budget['pct']}%)")

    quota = quota_mod.check_quota(cfg, tenant_id)
    if not quota["allowed"]:
        reasons.append(f"quota: {quota['denied_reason']}")

    api_key = _runpod_api_key(cfg)
    if not api_key:
        reasons.append("RUNPOD_API_KEY not set")

    gpu_info = FLASH_GPU_TYPES.get(gpu)
    if not gpu_info:
        reasons.append(f"unknown GPU type {gpu!r}")

    return {
        "allowed": len(reasons) == 0,
        "tenant_id": tenant_id,
        "gpu": gpu,
        "gpu_info": gpu_info,
        "budget": budget,
        "quota": quota,
        "reasons": reasons,
    }


def list_gpu_types() -> list[dict[str, Any]]:
    """List available GPU types with pricing."""
    return [
        {
            "name": name,
            "gpu_type": info["gpu_type"],
            "cost_per_sec": info["cost_per_sec"],
            "cost_per_hr": info["cost_per_hr"],
            "vram_mb": info["vram_mb"],
        }
        for name, info in FLASH_GPU_TYPES.items()
    ]


def provision_pod(
    cfg: Config,
    tenant_id: str,
    gpu: str = "A100",
    gpu_count: int = 1,
    container_disk_gb: int = 20,
    min_vcpu: int = 4,
    min_memory_gb: int = 16,
) -> dict[str, Any]:
    """Provision a persistent RunPod pod for training.

    Returns pod details including the pod ID for later termination.
    """
    decision = can_burst(cfg, tenant_id, gpu)
    if not decision["allowed"]:
        return {"error": "burst blocked", "reasons": decision["reasons"], "decision": decision}

    api_key = _runpod_api_key(cfg)
    if not api_key:
        return {"error": "RUNPOD_API_KEY not set"}

    gpu_info = FLASH_GPU_TYPES[gpu]

    mutation = """
    mutation ProvisionPod($input: PodFindAndDeployOnDemandInput!) {
        podFindAndDeployOnDemand(input: $input) {
            id
            name
            machineId
            gpuCount
            costPerHr
            desiredStatus
            imageName
        }
    }
    """

    variables = {
        "input": {
            "gpuTypeId": gpu_info["gpu_type"],
            "gpuCount": gpu_count,
            "containerDiskInGb": container_disk_gb,
            "minVcpuCount": min_vcpu,
            "minMemoryInGb": min_memory_gb,
            "name": f"apprentice-burst-{tenant_id}",
        }
    }

    try:
        data = _graphql(mutation, api_key, variables)
        pod_data = data.get("data", {}).get("podFindAndDeployOnDemand", {})
        if not pod_data:
            return {"error": "pod provisioning returned empty result"}
    except RuntimeError as e:
        LOG.error("pod provisioning failed: %s", e)
        return {"error": str(e)}

    pod_id = pod_data.get("id", "")
    cost_per_hr = float(pod_data.get("costPerHr", 0))
    log_msg = (
        f"provisioned pod {pod_id} ({pod_data.get('name')}) for tenant {tenant_id}: "
        f"${cost_per_hr:.4f}/hr, {gpu_info['gpu_type']}"
    )
    LOG.info(log_msg)

    budget_mod.record(cfg, tenant_id, 0.0, source="pod_provision")

    return {
        "pod_id": pod_id,
        "pod_name": pod_data.get("name"),
        "gpu_type": gpu_info["gpu_type"],
        "gpu_count": gpu_count,
        "cost_per_hr": cost_per_hr,
        "status": pod_data.get("desiredStatus"),
        "log": log_msg,
    }


def terminate_pod(cfg: Config, tenant_id: str, pod_id: str) -> dict[str, Any]:
    """Terminate a RunPod pod by ID."""
    api_key = _runpod_api_key(cfg)
    if not api_key:
        return {"error": "RUNPOD_API_KEY not set"}

    mutation = """
    mutation TerminatePod($input: PodTerminateInput!) {
        podTerminate(input: $input)
    }
    """

    variables = {"input": {"podId": pod_id}}

    try:
        data = _graphql(mutation, api_key, variables)
    except RuntimeError as e:
        LOG.error("pod termination failed: %s", e)
        return {"error": str(e)}

    LOG.info("terminated pod %s for tenant %s", pod_id, tenant_id)
    return {"pod_id": pod_id, "terminated": True, "response": data}


def list_pods(cfg: Config, tenant_id: str) -> list[dict[str, Any]]:
    """List all pods for the authenticated user."""
    api_key = _runpod_api_key(cfg)
    if not api_key:
        return []

    query = """
    query ListPods {
        myself {
            pods {
                id
                name
                costPerHr
                desiredStatus
                runtime {
                    uptimeInSeconds
                    gpus { gpuUtilPercent memoryUtilPercent }
                }
            }
        }
    }
    """

    try:
        data = _graphql(query, api_key)
    except RuntimeError as e:
        LOG.error("list pods failed: %s", e)
        return []

    pods_data = data.get("data", {}).get("myself", {}).get("pods", [])
    result = []
    for p in pods_data:
        uptime_hrs = (p.get("runtime", {}).get("uptimeInSeconds", 0) or 0) / 3600.0
        cost_hr = float(p.get("costPerHr", 0))
        result.append({
            "pod_id": p.get("id"),
            "name": p.get("name"),
            "status": p.get("desiredStatus"),
            "cost_per_hr": cost_hr,
            "uptime_hours": round(uptime_hrs, 2),
            "accrued_cost": round(cost_hr * uptime_hrs, 6),
        })
    return result


def record_run(
    cfg: Config,
    tenant_id: str,
    pattern_id: str,
    runtime_seconds: float,
    gpu: str = "A100",
) -> dict[str, Any]:
    """Record a completed training run's cost against the tenant budget."""
    gpu_info = FLASH_GPU_TYPES.get(gpu, FLASH_GPU_TYPES["A100"])
    cost = runtime_seconds * gpu_info["cost_per_sec"]
    hours = runtime_seconds / 3600.0

    budget_mod.record(cfg, tenant_id, cost, source=f"training:{pattern_id}")
    quota_mod.record_training_hours(cfg, tenant_id, hours)

    LOG.info(
        "burst run recorded: tenant=%s pattern=%s gpu=%s runtime=%.1fs cost=$%.6f",
        tenant_id, pattern_id, gpu, runtime_seconds, cost,
    )

    return {
        "tenant_id": tenant_id,
        "pattern_id": pattern_id,
        "gpu": gpu,
        "runtime_seconds": round(runtime_seconds, 1),
        "cost_usd": round(cost, 6),
        "training_hours": round(hours, 3),
    }
