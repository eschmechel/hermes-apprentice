"""Face 2 — conversational dispatcher (FastMCP).

Lets Hermes drive training as tool calls ("kick off training for pattern X,
tell me when it's done") rather than a Markdown procedure. Per the MCP-vs-Skill
split: the per-request *routing* stays a Skill (Hermes' selector handles it);
the stateful, lifecycled training dispatcher is MCP-shaped.

Run: ``apprentice-orchestrator-mcp`` (needs the ``mcp`` extra:
``uv pip install -e ./orchestrator[mcp]``). Register it as a Hermes MCP server.
"""

from __future__ import annotations

import json
import logging
import shutil
import subprocess
from dataclasses import asdict
from pathlib import Path

from . import budget as budget_mod, cost as cost_mod, jobs, quota as quota_mod, requests
from .config import Config
from .jobs import JobState

LOG = logging.getLogger("apprentice_orchestrator.mcp")


def _build_server():
    try:
        from mcp.server.fastmcp import FastMCP
    except ImportError as e:  # pragma: no cover - exercised only without the extra
        raise SystemExit(
            "the 'mcp' extra is required: uv pip install -e ./orchestrator[mcp]"
        ) from e

    mcp = FastMCP("apprentice-orchestrator")
    cfg = Config()

    @mcp.tool()
    def dispatch_training(pattern_id: str) -> dict:
        """Start a training+validation run for an approved pattern. Returns a
        job_id immediately; poll job_status for progress (runs in background)."""
        job = JobState(job_id=jobs.new_job_id(pattern_id), pattern_id=pattern_id)
        job.save(cfg.jobs_dir)
        # Decoupled: enqueue a durable request; the always-on watcher executes
        # it (and applies the placement policy). Survives MCP restarts.
        requests.enqueue(cfg, job.job_id, pattern_id)
        return {"job_id": job.job_id, "status": job.status, "queued": True}

    @mcp.tool()
    def job_status(job_id: str) -> dict:
        """Current step + verdict for a dispatched training job."""
        job = jobs.load_job(cfg.jobs_dir, job_id)
        if not job:
            return {"error": f"no job {job_id}"}
        return asdict(job)

    @mcp.tool()
    def list_pattern_candidates() -> list[dict]:
        """Patterns the detector has emitted (id + status + record_count)."""
        out = []
        if cfg.patterns_dir.is_dir():
            for manifest in sorted(cfg.patterns_dir.glob("*/manifest.json")):
                try:
                    data = json.loads(manifest.read_text(encoding="utf-8"))
                except (json.JSONDecodeError, OSError):
                    continue
                out.append({
                    "pattern_id": data.get("id", manifest.parent.name),
                    "status": data.get("status"),
                    "record_count": data.get("record_count"),
                    "description": data.get("description"),
                })
        return out

    @mcp.tool()
    def get_roi(pattern_id: str = "") -> dict | list[dict]:
        """ROI snapshot. With pattern_id returns a single pattern dict
        (train_cost, saved, roi, broke_even, runs, broke_even_at).
        Without pattern_id returns all patterns as a list."""
        if pattern_id:
            return cost_mod.roi(cfg, pattern_id)
        return cost_mod.all_patterns_roi(cfg)

    @mcp.tool()
    def get_usage(pattern_id: str = "", bucket: str = "day") -> list[dict]:
        """Usage over time for specialist requests. Buckets: hour, day, week.
        Returns [{time, requests, cost_saved}, ...]."""
        pid = pattern_id if pattern_id else None
        return cost_mod.usage_over_time(cfg, pid, bucket=bucket)

    @mcp.tool()
    def propose_merge(parent_a: str, parent_b: str, merged_id: str = "",
                      description: str = "") -> dict:
        """Propose merging two existing patterns into one combined specialist.
        Merges the datasets, notifies the operator for approval, and on
        'train gc-...' reply runs the full training pipeline on the merged
        dataset.
        Returns the merge proposal status."""
        if not merged_id:
            merged_id = f"{parent_a}+{parent_b}"
        if not description:
            description = f"Merged specialist: {parent_a} + {parent_b}"

        # 1. Run dataset-builder merge to produce the merged dataset.
        db = shutil.which("dataset-builder")
        if not db:
            return {"error": "dataset-builder not found on PATH", "merged_id": merged_id}

        out_dir = str(cfg.root / "datasets")
        argv = [db, "merge",
                "--pattern-a", parent_a,
                "--pattern-b", parent_b,
                "--merged-id", merged_id,
                "--output-dir", out_dir]
        if cfg.observer_url:
            argv += ["--observer-url", cfg.observer_url]
        if cfg.presidio_url:
            argv += ["--presidio-url", cfg.presidio_url]

        try:
            proc = subprocess.run(argv, capture_output=True, text=True, timeout=300)
            if proc.returncode != 0:
                return {"error": f"dataset merge failed: {proc.stderr[-500:]}",
                        "merged_id": merged_id, "returncode": proc.returncode}
        except subprocess.TimeoutExpired:
            return {"error": "dataset merge timed out (300s)", "merged_id": merged_id}

        # Count records from the merged dataset manifest.
        merged_dir = cfg.datasets_dir(merged_id)
        latest = _latest_version_dir(merged_dir)
        record_count = 0
        if latest:
            manifest = latest / "manifest.json"
            if manifest.exists():
                try:
                    data = json.loads(manifest.read_text(encoding="utf-8"))
                    record_count = data.get("total_count", 0)
                except (OSError, json.JSONDecodeError):
                    pass

        # 2. Notify the operator about the merge candidate.
        from . import candidates, notify as notify_mod

        record_count_val = record_count or 0
        cid = candidates.write(cfg, merged_id, salt="merge",
                               dataset_dir=str(latest) if latest else None)
        tg = shutil.which("apprentice-telegram")
        if tg:
            argv_enq = [tg, "enqueue", "graduation",
                        "--pattern-id", merged_id,
                        "--record-count", str(record_count_val),
                        "--description", description,
                        "--outbox-root", str(cfg.outbox_dir)]
            proc2 = subprocess.run(argv_enq, capture_output=True, text=True)
            if proc2.returncode != 0:
                LOG.error("merge graduation enqueue failed", extra={"error": proc2.stderr[-300:], "cid": cid})

        return {
            "merged_id": merged_id,
            "cid": cid,
            "parents": [parent_a, parent_b],
            "record_count": record_count_val,
            "dataset_dir": str(latest) if latest else None,
            "status": "proposed",
            "next_step": "Operator replies `train <cid>` to approve and run pipeline.",
        }

    @mcp.tool()
    def get_latency() -> dict:
        """Specialist vs upstream latency stats (count, avg, p50, p95, p99)."""
        return cost_mod.proxy_latency_stats(cfg)

    # ── tenant / quota / budget tools ──────────────────────────────────────

    @mcp.tool()
    def list_tenants() -> list[str]:
        """List all registered tenants with quota files."""
        return quota_mod.list_tenants(cfg)

    @mcp.tool()
    def get_quota(tenant_id: str) -> dict:
        """Resource quota status for a tenant (max LORAs, VRAM, training hours)."""
        return quota_mod.check_quota(cfg, tenant_id)

    @mcp.tool()
    def set_quota(tenant_id: str, max_loras: int | None = None,
                  max_vram_mb: int | None = None,
                  max_training_hours_monthly: float | None = None) -> dict:
        """Update quota limits for a tenant. Only provided fields are changed."""
        overrides = {}
        if max_loras is not None:
            overrides["max_loras"] = max_loras
        if max_vram_mb is not None:
            overrides["max_vram_mb"] = max_vram_mb
        if max_training_hours_monthly is not None:
            overrides["max_training_hours_monthly"] = max_training_hours_monthly
        return quota_mod.set_quota(cfg, tenant_id, **overrides)

    @mcp.tool()
    def get_budget(tenant_id: str) -> dict:
        """Monthly budget status for a tenant (spent, remaining, threshold)."""
        return budget_mod.get_budget(cfg, tenant_id)

    @mcp.tool()
    def set_budget(tenant_id: str, monthly_budget_usd: float) -> dict:
        """Set the monthly budget cap for a tenant in USD."""
        return budget_mod.set_budget(cfg, tenant_id, monthly_budget_usd)

    @mcp.tool()
    def budget_increase(tenant_id: str, additional_usd: float) -> dict:
        """Increase the monthly budget cap by *additional_usd*."""
        return budget_mod.budget_increase(cfg, tenant_id, additional_usd)

    @mcp.tool()
    def budget_history(tenant_id: str, limit: int = 20) -> list[dict]:
        """Recent budget ledger entries for a tenant (newest first)."""
        return budget_mod.budget_history(cfg, tenant_id, limit=limit)

    return mcp


def _latest_version_dir(parent: Path) -> Path | None:
    if not parent.is_dir():
        return None
    versions = []
    for p in parent.iterdir():
        if p.is_dir() and p.name.startswith("v") and p.name[1:].isdigit():
            versions.append((int(p.name[1:]), p))
    if not versions:
        return None
    return max(versions, key=lambda t: t[0])[1]


def main() -> int:
    _build_server().run(transport="stdio")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
