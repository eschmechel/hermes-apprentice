"""Durable training-request queue (W3 decouple).

The MCP ``dispatch_training`` tool enqueues a request here and returns
immediately; the always-on watcher drains the queue and runs the pipeline. This
survives MCP-server restarts (the pipeline no longer runs inside the MCP
process) and unifies the conversational + autonomous faces on one executor.
"""

from __future__ import annotations

import json
import time
from pathlib import Path

from .config import Config


def _now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def enqueue(cfg: Config, job_id: str, pattern_id: str) -> Path:
    d = cfg.job_requests_dir
    d.mkdir(parents=True, exist_ok=True)
    path = d / f"{job_id}.json"
    path.write_text(json.dumps(
        {"job_id": job_id, "pattern_id": pattern_id, "requested_at": _now()},
        indent=2,
    ), encoding="utf-8")
    return path


def pending(cfg: Config) -> list[Path]:
    d = cfg.job_requests_dir
    if not d.is_dir():
        return []
    return sorted(p for p in d.glob("*.json") if p.is_file())


def ack(cfg: Config, req: Path) -> None:
    done = cfg.job_requests_dir / "processed"
    done.mkdir(parents=True, exist_ok=True)
    req.replace(done / req.name)
