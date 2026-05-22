"""`apprentice-orchestrator` — watcher tick, manual run, notify, status."""

from __future__ import annotations

import argparse
import json
import logging
import sys

from . import jobs, notify as notify_mod, watcher
from .config import Config
from .pipeline import run_pipeline
from . import cost as cost_mod


def _setup_logging(verbose: bool) -> None:
    logging.basicConfig(
        level=logging.DEBUG if verbose else logging.INFO,
        format='{"level":"%(levelname)s","component":"%(name)s","msg":"%(message)s"}',
        stream=sys.stderr,
    )


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-orchestrator",
        description="Drive training approvals through to promoted specialists.",
    )
    p.add_argument("-v", "--verbose", action="store_true")
    sub = p.add_subparsers(dest="cmd", required=True)

    sub.add_parser("tick", help="Process pending decision markers (run from cron).")

    r = sub.add_parser("run", help="Run the pipeline for a pattern now (manual trigger).")
    r.add_argument("--pattern-id", required=True)
    r.add_argument("--dataset-dir", default=None, help="Reuse an existing dataset dir instead of building.")

    n = sub.add_parser("notify", help="Enqueue a graduation + record the cid->pattern mapping.")
    n.add_argument("--pattern-id", required=True)
    n.add_argument("--record-count", type=int, required=True)
    n.add_argument("--description", required=True)
    n.add_argument("--example", action="append", default=[], dest="examples")

    s = sub.add_parser("status", help="Show a job's state (or list recent jobs).")
    s.add_argument("--job-id", default=None)

    c = sub.add_parser("cost", help="Print ROI analysis for one or all patterns.")
    c.add_argument("--pattern-id", default=None)
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    _setup_logging(args.verbose)
    cfg = Config()

    if args.cmd == "tick":
        print(json.dumps(watcher.tick(cfg), indent=2))
        return 0

    if args.cmd == "run":
        job = run_pipeline(args.pattern_id, cfg=cfg, dataset_dir=args.dataset_dir)
        print(json.dumps({"job_id": job.job_id, "status": job.status,
                          "result": job.result, "error": job.error}, indent=2))
        return 0 if job.status == jobs.STATUS_PASSED else 1

    if args.cmd == "notify":
        cid = notify_mod.notify(cfg, args.pattern_id, record_count=args.record_count,
                                description=args.description, examples=args.examples)
        print(json.dumps({"pattern_id": args.pattern_id, "cid": cid}, indent=2))
        return 0

    if args.cmd == "status":
        if args.job_id:
            job = jobs.load_job(cfg.jobs_dir, args.job_id)
            if not job:
                print(json.dumps({"error": f"no job {args.job_id}"}))
                return 1
            from dataclasses import asdict
            print(json.dumps(asdict(job), indent=2))
            return 0
        ids = sorted(p.stem for p in cfg.jobs_dir.glob("*.json")) if cfg.jobs_dir.is_dir() else []
        print(json.dumps({"jobs": ids}, indent=2))
        return 0

    if args.cmd == "cost":
        if args.pattern_id:
            result = cost_mod.roi(cfg, args.pattern_id)
        else:
            result = cost_mod.all_patterns_roi(cfg)
        print(json.dumps(result, indent=2))
        return 0

    return 2


if __name__ == "__main__":
    raise SystemExit(main())

