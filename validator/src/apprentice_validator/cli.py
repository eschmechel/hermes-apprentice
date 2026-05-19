"""Validator CLI — ``apprentice-validate`` entrypoint.

Evaluates a merged specialist model against a held-out test set, computes
metrics vs. the raw base model, and outputs a pass/fail verdict.  On pass the
model is promoted into the Apprentice registry; on fail a structured report
is saved for post-mortem analysis.

Usage::

    apprentice-validate \\
        --model-dir     ~/apprentice/merged/<pattern-id>/v1 \\
        --test-dataset  ~/apprentice/datasets/<pattern-id>/v1/test.jsonl.gz \\
        --pattern-id    <pattern-id> \\
        [--baseline-model Qwen/Qwen2.5-1.5B-Instruct] \\
        [--teacher-score 85.0] \\
        [--max-tokens 256] \\
        [--check-only]
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import time
from pathlib import Path
from typing import Any

from . import baseline_runner, failure_reporter, metrics, promotion_gate, registry
from .logging import setup_logging
from .test_runner import check_only as specialist_check_only

LOG = logging.getLogger("apprentice_validator")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-validate",
        description="Evaluate a merged specialist model against a held-out test set.",
    )
    p.add_argument("--model-dir", required=True,
                   help="Path to the merged specialist model (from apprentice-merge).")
    p.add_argument("--test-dataset", required=True,
                   help="Path to test.jsonl.gz (from dataset-builder splitter).")
    p.add_argument("--pattern-id", required=True,
                   help="Pattern/skill identifier for registry naming.")
    p.add_argument("--baseline-model", default="Qwen/Qwen2.5-1.5B-Instruct",
                   help="HF model id for the baseline (default: raw Qwen2.5-1.5B-Instruct).")
    p.add_argument("--teacher-score", type=float, default=None,
                   help="Optional teacher F1 score for informational reporting.")
    p.add_argument("--max-tokens", type=int, default=256,
                   help="Max tokens for inference (default: 256).")
    p.add_argument("--gpu-memory-utilization", type=float, default=0.90,
                   help="vLLM GPU memory fraction (default: 0.90).")
    p.add_argument("--registry-root", default=None,
                   help="Override registry root (default: ~/apprentice/registry).")
    p.add_argument("--failures-dir", default=None,
                   help="Override failures dir (default: ~/apprentice/failures).")
    p.add_argument("--check-only", action="store_true",
                   help="Validate args + dataset without running inference.")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def run_validate(args: argparse.Namespace) -> int:
    model_dir = Path(args.model_dir).expanduser().resolve()
    test_dataset = Path(args.test_dataset).expanduser().resolve()
    pattern_id = args.pattern_id

    # Verify training manifest before touching the GPU.
    from apprentice_trainer.manifest_signer import verify_manifest
    manifest_path = model_dir / "training_manifest.json"
    if not manifest_path.exists():
        LOG.warning("no training manifest found — continuing without signature check",
                    extra={"model_dir": str(model_dir)})
    elif not verify_manifest(manifest_path):
        LOG.error(
            "training manifest signature INVALID — refusing to evaluate. "
            "The model may not have been produced by an authorized trainer.",
            extra={"manifest": str(manifest_path)},
        )
        return 2

    LOG.info("validator starting", extra={
        "model_dir": str(model_dir),
        "test_dataset": str(test_dataset),
        "pattern_id": pattern_id,
        "baseline_model": args.baseline_model,
        "max_tokens": args.max_tokens,
    })

    # ---- specialist --------------------------------------------------------
    t0 = time.time()
    from .test_runner import run_specialist
    try:
        specialist_pairs = run_specialist(
            model_dir=model_dir,
            test_dataset=test_dataset,
            max_tokens=args.max_tokens,
            gpu_memory_utilization=args.gpu_memory_utilization,
        )
    except RuntimeError as e:
        LOG.error("specialist inference failed", extra={"error": str(e)})
        return 3
    specialist_scores = metrics.compute_metrics(specialist_pairs)

    # ---- baseline ---------------------------------------------------------
    t1 = time.time()
    try:
        baseline_pairs = baseline_runner.run_baseline(
            test_dataset=test_dataset,
            base_model=args.baseline_model,
            max_tokens=args.max_tokens,
            gpu_memory_utilization=args.gpu_memory_utilization,
        )
    except RuntimeError as e:
        LOG.error("baseline inference failed", extra={"error": str(e)})
        return 3
    baseline_scores = metrics.compute_metrics(baseline_pairs)

    # ---- compare + gate ---------------------------------------------------
    comparison = metrics.compare_metrics(specialist_scores, baseline_scores)
    verdict = promotion_gate.evaluate(comparison, teacher_score=args.teacher_score)

    elapsed = time.time() - t0

    # ---- output -----------------------------------------------------------
    result: dict[str, Any] = {
        "pattern_id": pattern_id,
        "specialist_scores": specialist_scores,
        "baseline_scores": baseline_scores,
        "comparison": comparison,
        "verdict": verdict,
        "wallclock_seconds": round(elapsed, 1),
    }

    if verdict["passed"]:
        reg_root = Path(args.registry_root) if args.registry_root else None
        try:
            dest = registry.promote(
                pattern_id=pattern_id,
                model_dir=model_dir,
                scores=comparison,
                registry_root=reg_root,
            )
            result["promoted_to"] = str(dest)
        except RuntimeError as e:
            LOG.error("promotion failed", extra={"error": str(e)})
            result["promotion_error"] = str(e)
            _print_result(result)
            return 4
    else:
        failures_root = Path(args.failures_dir) if args.failures_dir else None
        try:
            report_path = failure_reporter.report(
                pattern_id=pattern_id,
                model_dir=model_dir,
                test_dataset=test_dataset,
                verdict=verdict,
                failures_dir=failures_root,
            )
            result["failure_report"] = str(report_path)
        except OSError as e:
            LOG.error("failure report write failed", extra={"error": str(e)})

    _print_result(result)
    return 0 if verdict["passed"] else 1


def _print_result(result: dict[str, Any]) -> None:
    """Emit the final result dict as JSON to stdout."""
    sys.stdout.write(json.dumps(result, indent=2, sort_keys=True, default=str, ensure_ascii=False))
    sys.stdout.write("\n")


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    setup_logging(logging.DEBUG if args.verbose else logging.INFO)

    if args.check_only:
        return specialist_check_only(
            test_dataset=Path(args.test_dataset).expanduser().resolve(),
            model_dir=Path(args.model_dir).expanduser().resolve(),
        )

    return run_validate(args)


if __name__ == "__main__":
    raise SystemExit(main())
