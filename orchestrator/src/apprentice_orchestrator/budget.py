"""Monthly monetary budget with threshold alerts (Phase 2e).

Tracks cloud spend per tenant via an append-only JSONL ledger at
``<root>/tenants/<tenant>/budget.jsonl``.  Three alert thresholds:

- **80%** — warning: cloud spend is high, reply ``budget increase <amt>``
- **95%** — critical: cloud paused, reply to resume
- **100%** — exhausted: all cloud blocked

Usage::

    from apprentice_orchestrator import budget
    status = budget.check_budget(cfg, "acme-corp")
    # {"allowed": True, "pct": 42.0, "spent": 8.40, "budget": 20.0, "threshold": None}
"""

from __future__ import annotations

import json
import logging
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

LOG = logging.getLogger("apprentice_orchestrator.budget")

BUDGET_FILE = "budget.jsonl"
CONFIG_FILE = "budget_config.json"

# Default monthly budget in USD.
DEFAULT_MONTHLY_USD = 20.0


def _current_month() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m")


def _budget_ledger_path(cfg, tenant_id: str) -> Path:
    return cfg.root / "tenants" / tenant_id / BUDGET_FILE


def _config_path(cfg, tenant_id: str) -> Path:
    return cfg.root / "tenants" / tenant_id / CONFIG_FILE


def _load_config(cfg, tenant_id: str) -> dict[str, Any]:
    path = _config_path(cfg, tenant_id)
    if not path.exists():
        return {"monthly_budget_usd": DEFAULT_MONTHLY_USD, "billing_month": _current_month()}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {"monthly_budget_usd": DEFAULT_MONTHLY_USD, "billing_month": _current_month()}
    # Auto-reset on month rollover.
    if data.get("billing_month") != _current_month():
        data["billing_month"] = _current_month()
        data["monthly_budget_usd"] = data.get("monthly_budget_usd", DEFAULT_MONTHLY_USD)
        _save_config(cfg, tenant_id, data)
    return data


def _save_config(cfg, tenant_id: str, data: dict[str, Any]) -> None:
    path = _config_path(cfg, tenant_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def _current_month_spend(cfg, tenant_id: str) -> float:
    """Sum of all spend entries in the current billing month."""
    path = _budget_ledger_path(cfg, tenant_id)
    if not path.exists():
        return 0.0
    month = _current_month()
    total = 0.0
    try:
        with open(path) as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    entry = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if entry.get("month") == month:
                    total += float(entry.get("amount_usd", 0))
    except OSError:
        pass
    return round(total, 6)


def record(cfg, tenant_id: str, amount_usd: float, source: str = "training") -> dict[str, Any]:
    """Record a cloud spend entry for the tenant.

    Args:
        tenant_id: The tenant identifier.
        amount_usd: Amount in USD (positive).
        source: Description of what the spend was for (e.g. "training", "burst").

    Returns the latest budget status.
    """
    path = _budget_ledger_path(cfg, tenant_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    entry = {
        "ts": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "month": _current_month(),
        "tenant_id": tenant_id,
        "amount_usd": round(amount_usd, 6),
        "source": source,
    }
    with open(path, "a") as fh:
        fh.write(json.dumps(entry, sort_keys=True) + "\n")
        fh.flush()
    return check_budget(cfg, tenant_id)


def get_budget(cfg, tenant_id: str) -> dict[str, Any]:
    """Current budget state for a tenant (read-only)."""
    config = _load_config(cfg, tenant_id)
    spent = _current_month_spend(cfg, tenant_id)
    budget = config.get("monthly_budget_usd", DEFAULT_MONTHLY_USD)
    pct = round(spent / budget * 100, 1) if budget > 0 else 0.0
    threshold = _resolve_threshold(pct)
    return {
        "tenant_id": tenant_id,
        "budget": budget,
        "spent": spent,
        "remaining": round(budget - spent, 6),
        "pct": pct,
        "threshold": threshold,
        "billing_month": config.get("billing_month", _current_month()),
        "allowed": threshold != "exhausted",
    }


def check_budget(cfg, tenant_id: str) -> dict[str, Any]:
    """Check budget before cloud spend. Returns status dict with ``allowed``."""
    return get_budget(cfg, tenant_id)


def set_budget(cfg, tenant_id: str, monthly_budget_usd: float) -> dict[str, Any]:
    """Set the monthly budget cap for a tenant."""
    config = _load_config(cfg, tenant_id)
    config["monthly_budget_usd"] = round(float(monthly_budget_usd), 2)
    _save_config(cfg, tenant_id, config)
    return get_budget(cfg, tenant_id)


def budget_increase(cfg, tenant_id: str, additional_usd: float) -> dict[str, Any]:
    """Increase the monthly budget cap by *additional_usd*.

    Equivalent to ``set_budget(cfg, tenant_id, current + additional_usd)``.
    """
    config = _load_config(cfg, tenant_id)
    current = config.get("monthly_budget_usd", DEFAULT_MONTHLY_USD)
    return set_budget(cfg, tenant_id, current + additional_usd)


def budget_history(cfg, tenant_id: str, limit: int = 20) -> list[dict[str, Any]]:
    """Recent budget ledger entries, newest first."""
    path = _budget_ledger_path(cfg, tenant_id)
    if not path.exists():
        return []
    entries = []
    try:
        with open(path) as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    entries.append(json.loads(line))
                except json.JSONDecodeError:
                    continue
    except OSError:
        pass
    entries.sort(key=lambda e: (e.get("ts", ""), e.get("source", "")), reverse=True)
    return entries[:limit]


def _resolve_threshold(pct: float) -> str | None:
    """Return the threshold name based on percentage spent."""
    if pct >= 100.0:
        return "exhausted"
    if pct >= 95.0:
        return "critical"
    if pct >= 80.0:
        return "warning"
    return None
