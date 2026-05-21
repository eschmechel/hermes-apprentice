"""Face 1 — autonomous decisions watcher.

``tick`` scans ``~/.apprentice/decisions/`` oldest-first for the markers the
Telegram reply-poller writes, and for each ``train`` marker runs the pipeline
to completion, then files the marker under ``decisions/processed/``. Designed to
be invoked from a host cron (mirroring the telegram crons); one job per tick
keeps the single GPU serialized. A job failure is recorded, never fatal to the
loop.
"""

from __future__ import annotations

import json
import logging
from pathlib import Path

from . import candidates, jobs, requests
from .config import Config
from .pipeline import Runner, run_pipeline

LOG = logging.getLogger("apprentice_orchestrator.watcher")


def _markers(decisions_dir: Path) -> list[Path]:
    if not decisions_dir.is_dir():
        return []
    return sorted(p for p in decisions_dir.glob("*.json") if p.is_file())


def _ack(cfg: Config, marker: Path) -> None:
    processed = cfg.decisions_dir / "processed"
    processed.mkdir(parents=True, exist_ok=True)
    marker.replace(processed / marker.name)


def tick(cfg: Config | None = None, *, runner: Runner | None = None, max_jobs: int = 1) -> dict:
    """Process pending decision markers. Returns a summary dict."""
    cfg = cfg or Config()
    summary = {"processed": [], "trained": [], "skipped": [], "errors": []}
    jobs_run = 0

    for marker in _markers(cfg.decisions_dir):
        try:
            data = json.loads(marker.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError) as e:
            summary["errors"].append({"marker": marker.name, "error": str(e)})
            continue

        action = (data.get("action") or "").lower()
        cid = data.get("cid")

        if action == "train":
            if jobs_run >= max_jobs:
                break  # leave the rest for the next tick (one GPU)
            pattern_id = candidates.resolve(cfg, cid) if cid else None
            if not pattern_id:
                summary["errors"].append({"marker": marker.name, "error": f"unresolved cid {cid!r}"})
                _ack(cfg, marker)
                continue
            LOG.info("training approved", extra={"pattern_id": pattern_id, "cid": cid})
            job = run_pipeline(pattern_id, cfg=cfg, runner=runner)
            jobs_run += 1
            summary["trained"].append({"pattern_id": pattern_id, "job_id": job.job_id, "status": job.status})
            _ack(cfg, marker)

        elif action == "skip":
            _set_pattern_status(cfg, cid, "rejected")
            summary["skipped"].append({"marker": marker.name, "cid": cid})
            _ack(cfg, marker)

        elif action == "details":
            # dataset preview is a best-effort stub for v1; just file the marker.
            _ack(cfg, marker)

        else:
            summary["errors"].append({"marker": marker.name, "error": f"unknown action {action!r}"})
            _ack(cfg, marker)

        summary["processed"].append(marker.name)

    # Drain the MCP/dispatch request queue (shares the one-GPU budget so the
    # decisions path and the conversational path can't both seize the GPU).
    for req in requests.pending(cfg):
        if jobs_run >= max_jobs:
            break
        try:
            data = json.loads(req.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError) as e:
            summary["errors"].append({"request": req.name, "error": str(e)})
            requests.ack(cfg, req)
            continue
        pattern_id = data.get("pattern_id")
        if not pattern_id:
            summary["errors"].append({"request": req.name, "error": "missing pattern_id"})
            requests.ack(cfg, req)
            continue
        existing = jobs.load_job(cfg.jobs_dir, data.get("job_id")) if data.get("job_id") else None
        LOG.info("training requested via dispatch", extra={"pattern_id": pattern_id, "job_id": data.get("job_id")})
        job = run_pipeline(pattern_id, cfg=cfg, runner=runner, job=existing)
        jobs_run += 1
        summary["trained"].append({"pattern_id": pattern_id, "job_id": job.job_id, "status": job.status})
        requests.ack(cfg, req)

    return summary


def _set_pattern_status(cfg: Config, cid: str | None, status: str) -> None:
    if not cid:
        return
    pid = candidates.resolve(cfg, cid)
    if not pid:
        return
    manifest = cfg.patterns_dir / pid / "manifest.json"
    if not manifest.exists():
        return
    try:
        data = json.loads(manifest.read_text(encoding="utf-8"))
        data["status"] = status
        manifest.write_text(json.dumps(data, indent=2), encoding="utf-8")
    except (json.JSONDecodeError, OSError) as e:
        LOG.warning("could not set pattern status", extra={"pattern_id": pid, "error": str(e)})
