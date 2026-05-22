"""`apprentice-orchestrator` — watcher tick, manual run, notify, status."""

from __future__ import annotations

import argparse
import json
import logging
import sys

from apprentice_trainer import models as trainer_models

from . import budget as budget_mod, flash_burst, jobs, notify as notify_mod, quota as quota_mod, watcher
from .config import Config
from .pipeline import run_pipeline
from . import cost as cost_mod
from . import safety


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

    list_models = sub.add_parser("list-models", help="List available base models.")
    list_models.add_argument("--show-4bit", action="store_true", help="Show quantized (4-bit) model IDs for training.")

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

    c = sub.add_parser("cost", help="Cost/ROI analysis: ledger ROI, usage over time, or latency stats.")
    c.add_argument("--pattern-id", default=None)
    c.add_argument("--usage", action="store_true", help="Show usage over time (specialist requests per bucket).")
    c.add_argument("--latency", action="store_true", help="Show specialist vs upstream latency stats.")
    c.add_argument("--bucket", default="day", choices=["hour", "day", "week"],
                   help="Time bucket for --usage (default: day).")

    sf = sub.add_parser("safety", help="Canary ramp management: list, status, advance, set-state, compare.")
    sf_sub = sf.add_subparsers(dest="safety_cmd", required=True)

    sf_list = sf_sub.add_parser("list", help="List all canary states.")
    sf_list.add_argument("--proxy-url", default=None)

    sf_st = sf_sub.add_parser("status", help="Get canary state for a pattern.")
    sf_st.add_argument("--pattern-id", required=True)
    sf_st.add_argument("--proxy-url", default=None)

    sf_adv = sf_sub.add_parser("advance", help="Trigger canary evaluation step.")
    sf_adv.add_argument("--pattern-id", required=True)
    sf_adv.add_argument("--score", type=float, default=None)
    sf_adv.add_argument("--proxy-url", default=None)

    sf_set = sf_sub.add_parser("set-state", help="Manually override canary state.")
    sf_set.add_argument("--pattern-id", required=True)
    sf_set.add_argument("--state", required=True, choices=safety.STATES)
    sf_set.add_argument("--pct", type=int, default=0)
    sf_set.add_argument("--proxy-url", default=None)

    sf_cmp = sf_sub.add_parser("compare", help="Compare two response bodies for agreement.")
    sf_cmp.add_argument("--specialist-body", required=True)
    sf_cmp.add_argument("--upstream-body", required=True)
    sf_cmp.add_argument("--proxy-url", default=None)

    sf_alert = sf_sub.add_parser("alert", help="Get alert message for a broken pattern.")
    sf_alert.add_argument("--pattern-id", required=True)
    sf_alert.add_argument("--proxy-url", default=None)

    # ── quota subcommand ────────────────────────────────────────────────────
    q = sub.add_parser("quota", help="Tenant resource quota management.")
    q_sub = q.add_subparsers(dest="quota_cmd", required=True)

    q_list = q_sub.add_parser("list", help="List all tenants.")
    q_get = q_sub.add_parser("get", help="Get quota for a tenant.")
    q_get.add_argument("--tenant-id", required=True)
    q_set = q_sub.add_parser("set", help="Update quota limits for a tenant.")
    q_set.add_argument("--tenant-id", required=True)
    q_set.add_argument("--max-loras", type=int, default=None)
    q_set.add_argument("--max-vram-mb", type=int, default=None)
    q_set.add_argument("--max-training-hours-monthly", type=float, default=None)

    # ── budget subcommand ───────────────────────────────────────────────────
    b = sub.add_parser("budget", help="Monthly budget management.")
    b_sub = b.add_subparsers(dest="budget_cmd", required=True)

    b_get = b_sub.add_parser("get", help="Get budget status for a tenant.")
    b_get.add_argument("--tenant-id", required=True)
    b_set = b_sub.add_parser("set", help="Set monthly budget cap.")
    b_set.add_argument("--tenant-id", required=True)
    b_set.add_argument("--monthly-budget-usd", type=float, required=True)
    b_inc = b_sub.add_parser("increase", help="Increase budget by amount.")
    b_inc.add_argument("--tenant-id", required=True)
    b_inc.add_argument("--additional-usd", type=float, required=True)
    b_hist = b_sub.add_parser("history", help="Show budget ledger entries.")
    b_hist.add_argument("--tenant-id", required=True)
    b_hist.add_argument("--limit", type=int, default=20)

    # ── burst subcommand ────────────────────────────────────────────────────
    bu = sub.add_parser("burst", help="RunPod cloud burst management.")
    bu_sub = bu.add_subparsers(dest="burst_cmd", required=True)

    bu_check = bu_sub.add_parser("check", help="Check if burst is allowed.")
    bu_check.add_argument("--tenant-id", default="default")
    bu_check.add_argument("--gpu", default="A100", choices=list(flash_burst.FLASH_GPU_TYPES.keys()))

    bu_list = bu_sub.add_parser("list", help="List available GPU types.")
    bu_prov = bu_sub.add_parser("provision", help="Provision a RunPod pod.")
    bu_prov.add_argument("--tenant-id", default="default")
    bu_prov.add_argument("--gpu", default="A100", choices=list(flash_burst.FLASH_GPU_TYPES.keys()))
    bu_prov.add_argument("--gpu-count", type=int, default=1)
    bu_term = bu_sub.add_parser("terminate", help="Terminate a RunPod pod.")
    bu_term.add_argument("--tenant-id", default="default")
    bu_term.add_argument("--pod-id", required=True)
    bu_pods = bu_sub.add_parser("list-pods", help="List active RunPod pods.")
    bu_pods.add_argument("--tenant-id", default="default")

    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    _setup_logging(args.verbose)
    cfg = Config()

    if args.cmd == "list-models":
        show_4bit = args.show_4bit
        for m in trainer_models.list_models():
            default_mark = " (default)" if m.get("default") else ""
            model_id = m["id"]
            if show_4bit and m.get("quantized_id"):
                model_id = m["quantized_id"]
            print(f"  {model_id}{default_mark}")
        return 0

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

    if args.cmd == "safety":
        proxy_url = args.proxy_url or cfg.proxy_url
        if args.safety_cmd == "list":
            print(json.dumps(safety.list_states(proxy_url), indent=2))
            return 0

        if args.safety_cmd == "status":
            result = safety.get_state(proxy_url, args.pattern_id)
            if not result:
                print(json.dumps({"error": f"pattern {args.pattern_id!r} not found"}))
                return 1
            print(json.dumps(result, indent=2))
            return 0

        if args.safety_cmd == "advance":
            result = safety.advance(proxy_url, args.pattern_id, score=args.score)
            if not result:
                print(json.dumps({"error": f"advance failed for {args.pattern_id!r}"}))
                return 1
            print(json.dumps(result, indent=2))
            return 0

        if args.safety_cmd == "set-state":
            ok = safety.set_state(proxy_url, args.pattern_id, args.state, pct=args.pct)
            if not ok:
                print(json.dumps({"error": "set-state failed"}))
                return 1
            print(json.dumps({"status": "ok"}))
            return 0

        if args.safety_cmd == "compare":
            result = safety.compare(proxy_url, args.specialist_body, args.upstream_body)
            if not result:
                print(json.dumps({"error": "compare failed"}))
                return 1
            print(json.dumps(result, indent=2))
            return 0

        if args.safety_cmd == "alert":
            msg = safety.alert(proxy_url, args.pattern_id)
            if not msg:
                print(json.dumps({"level": "info", "msg": f"pattern {args.pattern_id!r} is not broken"}))
                return 0
            print(json.dumps({"level": "alert", "msg": msg}))
            return 0

    if args.cmd == "cost":
        if args.usage:
            result = cost_mod.usage_over_time(cfg, args.pattern_id, bucket=args.bucket)
        elif args.latency:
            result = cost_mod.proxy_latency_stats(cfg)
        elif args.pattern_id:
            result = cost_mod.roi(cfg, args.pattern_id)
        else:
            result = cost_mod.all_patterns_roi(cfg)
        print(json.dumps(result, indent=2))
        return 0 if result else 1

    if args.cmd == "quota":
        if args.quota_cmd == "list":
            print(json.dumps(quota_mod.list_tenants(cfg), indent=2))
            return 0
        if args.quota_cmd == "get":
            result = quota_mod.get_quota(cfg, args.tenant_id)
            print(json.dumps(result, indent=2))
            return 0
        if args.quota_cmd == "set":
            overrides = {}
            if args.max_loras is not None:
                overrides["max_loras"] = args.max_loras
            if args.max_vram_mb is not None:
                overrides["max_vram_mb"] = args.max_vram_mb
            if args.max_training_hours_monthly is not None:
                overrides["max_training_hours_monthly"] = args.max_training_hours_monthly
            result = quota_mod.set_quota(cfg, args.tenant_id, **overrides)
            print(json.dumps(result, indent=2))
            return 0

    if args.cmd == "budget":
        if args.budget_cmd == "get":
            print(json.dumps(budget_mod.get_budget(cfg, args.tenant_id), indent=2))
            return 0
        if args.budget_cmd == "set":
            result = budget_mod.set_budget(cfg, args.tenant_id, args.monthly_budget_usd)
            print(json.dumps(result, indent=2))
            return 0
        if args.budget_cmd == "increase":
            result = budget_mod.budget_increase(cfg, args.tenant_id, args.additional_usd)
            print(json.dumps(result, indent=2))
            return 0
        if args.budget_cmd == "history":
            print(json.dumps(budget_mod.budget_history(cfg, args.tenant_id, limit=args.limit), indent=2))
            return 0

    if args.cmd == "burst":
        if args.burst_cmd == "list":
            result = flash_burst.list_gpu_types()
            print(json.dumps(result, indent=2))
            return 0
        tenant_id = getattr(args, "tenant_id", None) or cfg.tenant_id
        if args.burst_cmd == "check":
            result = flash_burst.can_burst(cfg, tenant_id, args.gpu)
            print(json.dumps(result, indent=2))
            return 0 if result["allowed"] else 1
        if args.burst_cmd == "provision":
            result = flash_burst.provision_pod(cfg, tenant_id, gpu=args.gpu, gpu_count=args.gpu_count)
            print(json.dumps(result, indent=2))
            return 0 if "pod_id" in result else 1
        if args.burst_cmd == "terminate":
            result = flash_burst.terminate_pod(cfg, tenant_id, args.pod_id)
            print(json.dumps(result, indent=2))
            return 0 if result.get("terminated") else 1
        if args.burst_cmd == "list-pods":
            result = flash_burst.list_pods(cfg, tenant_id)
            print(json.dumps(result, indent=2))
            return 0

    return 2


if __name__ == "__main__":
    raise SystemExit(main())

