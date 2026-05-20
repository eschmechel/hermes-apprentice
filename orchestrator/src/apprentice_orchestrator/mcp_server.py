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
import threading
from dataclasses import asdict

from . import jobs
from .config import Config
from .jobs import JobState
from .pipeline import run_pipeline


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
        threading.Thread(
            target=run_pipeline,
            args=(pattern_id,),
            kwargs={"cfg": cfg, "job": job},
            daemon=True,
        ).start()
        return {"job_id": job.job_id, "status": job.status}

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

    return mcp


def main() -> int:
    _build_server().run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
