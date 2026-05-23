"""Config + on-disk layout for the orchestrator.

All Apprentice state lives under ``~/.apprentice`` (override with
``APPRENTICE_ROOT``). Per-pattern artifacts are versioned ``v<N>`` directories,
mirroring what the caveat run produced by hand.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path

from apprentice_trainer import models as trainer_models


def apprentice_root() -> Path:
    return Path(os.environ.get("APPRENTICE_ROOT", Path.home() / ".apprentice")).expanduser()


def _env(name: str, default: str | None = None) -> str | None:
    v = os.environ.get(name)
    return v if v not in (None, "") else default


@dataclass
class Config:
    """Tunables for a pipeline run. Defaults match the validated demo run."""

    root: Path = field(default_factory=apprentice_root)
    base_model: str = field(default_factory=lambda: _resolve_base_model())
    # apprentice-train --profile (a YAML path). If None we omit the flag and let
    # the trainer fall back to APPRENTICE_TRAINER_PROFILE / its own defaults.
    train_profile: str | None = field(default_factory=lambda: _env("APPRENTICE_TRAIN_PROFILE"))
    max_steps: int = field(default_factory=lambda: int(_env("APPRENTICE_MAX_STEPS", "60")))
    gpu_memory_utilization: float = field(default_factory=lambda: float(_env("APPRENTICE_GPU_MEM_UTIL", "0.80")))
    # dataset-builder knobs (only used when we build a dataset rather than reuse one)
    observer_url: str | None = field(default_factory=lambda: _env("APPRENTICE_OBSERVER_URL"))
    presidio_url: str | None = field(default_factory=lambda: _env("APPRENTICE_PRESIDIO_URL"))
    proxy_url: str = field(default_factory=lambda: _env("APPRENTICE_PROXY_URL", "http://localhost:8083"))
    monthly_budget_usd: float = field(default_factory=lambda: float(
        _env("APPRENTICE_MONTHLY_BUDGET_USD", "20.0")))
    burst_gpu: str = field(default_factory=lambda: _env("APPRENTICE_BURST_GPU", "A100"))
    # Multi-tenant seam (W11). When set, all of this orchestrator's state +
    # artifacts live under ``<root>/tenants/<tenant_id>/`` so several tenants
    # share one host (and one warm base) without colliding. None/empty ==
    # single-tenant (the default layout, unchanged). Full authz/quotas/isolation
    # are v0.2.  APPRENTICE_TENANT is accepted as an alias of APPRENTICE_TENANT_ID
    # for parity with the W11 prototype.
    tenant_id: str | None = field(default_factory=lambda: _env("APPRENTICE_TENANT_ID") or _env("APPRENTICE_TENANT"))

    # Placement policy (VRAM arbiter). Autonomous runs use these without
    # blocking; interactive surfaces may override per-run (stretch).
    training_placement: str = field(default_factory=lambda: _env("APPRENTICE_TRAINING_PLACEMENT", "local"))
    train_vram_mb: int = field(default_factory=lambda: int(_env("APPRENTICE_TRAIN_VRAM_MB", "4000")))
    # What to do when a local run is requested but the GPU is busy (warm serve):
    # "evict" (stop the warm serve to free VRAM) | "cloud" (burst, stretch) | "skip".
    on_vram_conflict: str = field(default_factory=lambda: _env("APPRENTICE_ON_VRAM_CONFLICT", "evict"))

    @property
    def base(self) -> Path:
        """Tenant-namespaced root for this orchestrator's state + artifacts (W11).
        Equal to ``self.root`` when no tenant is set (single-tenant layout)."""
        return self.root / "tenants" / self.tenant_id if self.tenant_id else self.root

    # ---- per-pattern paths -------------------------------------------------
    def datasets_dir(self, pattern_id: str) -> Path:
        return self.base / "datasets" / pattern_id

    def checkpoints_dir(self, pattern_id: str, version: str) -> Path:
        return self.base / "checkpoints" / pattern_id / version

    def merged_dir(self, pattern_id: str, version: str) -> Path:
        return self.base / "merged" / pattern_id / version

    def baseline_path(self, pattern_id: str, version: str) -> Path:
        return self.base / "baselines" / f"{pattern_id}-{version}.jsonl"

    # ---- cost / ROI --------------------------------------------------------
    gpu_hourly_usd: float = field(default_factory=lambda: float(
        _env("APPRENTICE_GPU_HOURLY_USD", "0.40")))
    proxy_log_glob: str = field(default_factory=lambda: _env(
        "APPRENTICE_PROXY_LOG_GLOB", ""))

    @property
    def cost_dir(self) -> Path:
        return self.base / "cost"

    def _resolve_proxy_log_glob(self) -> str:
        if self.proxy_log_glob:
            return self.proxy_log_glob
        # The proxy is a single shared process across tenants (it logs everyone's
        # traffic, keyed by tenant-namespaced pattern_id) — its log stays at root.
        return str(self.root / "proxy" / "proxy.log")

    # ---- queues / state ----------------------------------------------------
    @property
    def decisions_dir(self) -> Path:
        return self.base / "decisions"

    @property
    def outbox_dir(self) -> Path:
        return self.base / "outbox"

    @property
    def jobs_dir(self) -> Path:
        return self.base / "jobs"

    @property
    def job_requests_dir(self) -> Path:
        # Durable queue MCP dispatch writes and the watcher drains (decoupled
        # from the MCP process lifecycle).
        return self.base / "jobs" / "requests"

    @property
    def candidates_dir(self) -> Path:
        return self.base / "candidates"

    @property
    def patterns_dir(self) -> Path:
        return self.base / "patterns"


def namespaced_pattern_id(tenant: str | None, pattern_id: str) -> str:
    """Adapter-name isolation on the shared warm base (W11): a tenant's specialist
    is addressed as ``<tenant>--<pattern>`` so the proxy/serving layer keeps
    tenants' LoRA adapters distinct by name. Single-tenant returns the id as-is."""
    return f"{tenant}--{pattern_id}" if tenant else pattern_id


def _resolve_base_model() -> str:
    """Resolve APPRENTICE_BASE_MODEL (alias or full ID), falling back to default."""
    raw = _env("APPRENTICE_BASE_MODEL")
    if raw is None:
        return trainer_models.get_default_model()
    return trainer_models.resolve_model(raw, load_in_4bit=False)


def latest_version_dir(parent: Path) -> Path | None:
    """Return the highest ``v<N>`` subdir of ``parent`` (None if none exist)."""
    if not parent.is_dir():
        return None
    versions = []
    for p in parent.iterdir():
        if p.is_dir() and p.name.startswith("v") and p.name[1:].isdigit():
            versions.append((int(p.name[1:]), p))
    if not versions:
        return None
    return max(versions, key=lambda t: t[0])[1]
