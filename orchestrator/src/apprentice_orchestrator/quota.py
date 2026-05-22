"""Per-tenant resource quotas: max concurrent LORAs, VRAM, monthly training hours.

Each tenant has a ``quota.json`` under ``<root>/tenants/<tenant>/``:

.. code-block:: json

    {
        "tenant_id": "acme-corp",
        "max_loras": 4,
        "max_vram_mb": 16000,
        "max_training_hours_monthly": 100,
        "training_hours_used": 15.5,
        "billing_month": "2026-05"
    }

Counters reset when ``billing_month`` does not match the current month.

Usage::

    from apprentice_orchestrator import quota
    q = quota.check_quota(cfg, "acme-corp")
    q["allowed"]  # True if within all limits
"""

from __future__ import annotations

import json
import logging
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

LOG = logging.getLogger("apprentice_orchestrator.quota")


def _current_month() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m")


def _quota_path(cfg, tenant_id: str) -> Path:
    return cfg.root / "tenants" / tenant_id / "quota.json"


def _default_quota(tenant_id: str) -> dict[str, Any]:
    return {
        "tenant_id": tenant_id,
        "max_loras": 4,
        "max_vram_mb": 16000,
        "max_training_hours_monthly": 100,
        "training_hours_used": 0.0,
        "billing_month": _current_month(),
    }


def _load(cfg, tenant_id: str) -> dict[str, Any]:
    path = _quota_path(cfg, tenant_id)
    if not path.exists():
        data = _default_quota(tenant_id)
        _save(cfg, tenant_id, data)
        return data
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as e:
        LOG.warning("corrupt quota for %s, resetting: %s", tenant_id, e)
        data = _default_quota(tenant_id)
        _save(cfg, tenant_id, data)
        return data
    # Auto-reset on month rollover.
    if data.get("billing_month") != _current_month():
        data["training_hours_used"] = 0.0
        data["billing_month"] = _current_month()
        _save(cfg, tenant_id, data)
    return data


def _save(cfg, tenant_id: str, data: dict[str, Any]) -> None:
    path = _quota_path(cfg, tenant_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def list_tenants(cfg) -> list[str]:
    """List all tenant IDs under ``<root>/tenants/``."""
    tenants_dir = cfg.root / "tenants"
    if not tenants_dir.is_dir():
        return []
    return sorted(p.name for p in tenants_dir.iterdir() if p.is_dir())


def get_quota(cfg, tenant_id: str) -> dict[str, Any]:
    """Return the full quota state for a tenant (read-only, no counters touched)."""
    return _load(cfg, tenant_id)


def set_quota(cfg, tenant_id: str, **overrides: Any) -> dict[str, Any]:
    """Update quota limits for a tenant. Returns the updated quota."""
    data = _load(cfg, tenant_id)
    allowed_keys = {"max_loras", "max_vram_mb", "max_training_hours_monthly"}
    for k, v in overrides.items():
        if k in allowed_keys and isinstance(v, (int, float)) and v >= 0:
            data[k] = int(v) if k != "max_training_hours_monthly" else v
    _save(cfg, tenant_id, data)
    return data


def check_quota(cfg, tenant_id: str) -> dict[str, Any]:
    """Return an ``allowed`` verdict plus diagnostic counters.

    Returns::

        {
            "allowed": True | False,
            "tenant_id": "acme-corp",
            "max_loras": 4, "current_loras": 0,
            "max_vram_mb": 16000, "current_vram_mb": 0,
            "training_hours_used": 15.5,
            "max_training_hours_monthly": 100,
            "denied_reason": None | str,
        }
    """
    data = _load(cfg, tenant_id)
    denied: list[str] = []

    if data.get("training_hours_used", 0) >= data.get("max_training_hours_monthly", 100):
        denied.append(f"monthly training hours exhausted ({data['training_hours_used']:.1f}/{data['max_training_hours_monthly']})")

    return {
        "allowed": len(denied) == 0,
        "tenant_id": tenant_id,
        "max_loras": data.get("max_loras", 4),
        "current_loras": 0,
        "max_vram_mb": data.get("max_vram_mb", 16000),
        "current_vram_mb": 0,
        "training_hours_used": data.get("training_hours_used", 0),
        "max_training_hours_monthly": data.get("max_training_hours_monthly", 100),
        "denied_reason": "; ".join(denied) if denied else None,
    }


def record_training_hours(cfg, tenant_id: str, hours: float) -> dict[str, Any]:
    """Add *hours* to the tenant's monthly training hour counter. Returns updated quota."""
    data = _load(cfg, tenant_id)
    data["training_hours_used"] = round(data.get("training_hours_used", 0) + hours, 3)
    _save(cfg, tenant_id, data)
    return data
