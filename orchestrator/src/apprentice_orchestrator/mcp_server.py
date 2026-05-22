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
from dataclasses import asdict

from . import cost as cost_mod, jobs, requests
from .config import Config
from .jobs import JobState


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
    def get_latency() -> dict:
        """Specialist vs upstream latency stats (count, avg, p50, p95, p99)."""
        return cost_mod.proxy_latency_stats(cfg)

    return mcp


def main() -> int:
    _build_server().run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
