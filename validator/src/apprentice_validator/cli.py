"""Validator CLI — ``apprentice-validate`` entrypoint.

Evaluates a merged specialist model against a held-out test set, compares it
to a pre-computed baseline (``apprentice-baseline``), and outputs a pass/fail
verdict.  On pass the model is promoted into the Apprentice registry; on fail
a structured report is saved for post-mortem analysis.

The baseline is **not** computed here — run ``apprentice-baseline`` once per
(dataset, base_model) pair and pass its output via ``--baseline-pairs``.  This
keeps the specialist and base model from competing for GPU memory in a single
process and lets you re-validate many specialists against the same baseline
without recomputing it.

Usage::

    # Step 1 (once per dataset).
    apprentice-baseline \\
        --test-dataset  ~/.apprentice/datasets/<pattern-id>/v1/test.jsonl.gz \\
        --output        ~/.apprentice/baselines/<pattern-id>-v1.jsonl

    # Step 2 (per specialist).
    apprentice-validate \\
        --model-dir       ~/.apprentice/merged/<pattern-id>/v1 \\
        --test-dataset    ~/.apprentice/datasets/<pattern-id>/v1/test.jsonl.gz \\
        --pattern-id      <pattern-id> \\
        --baseline-pairs  ~/.apprentice/baselines/<pattern-id>-v1.jsonl \\
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

from . import baseline_io, failure_reporter, metrics, promotion_gate, registry, skill_registrar
from .logging import setup_logging
from .test_runner import check_only as specialist_check_only, load_test_dataset

# re-export for external use
from .registry import find_latest_version  # noqa: F401


def _apprentice_root() -> Path:
    return Path(os.environ.get("APPRENTICE_ROOT", Path.home() / ".apprentice")).expanduser()

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
    p.add_argument("--baseline-pairs", default=None,
                   help="Path to JSONL produced by apprentice-baseline. "
                        "Required unless --check-only.")
    p.add_argument("--teacher-score", type=float, default=None,
                   help="Optional teacher F1 score for informational reporting.")
    p.add_argument("--merge-regression", nargs=2, metavar=("PARENT_A", "PARENT_B"),
                   help="Parent pattern IDs for merge regression check. "
                        "When set, validates the merged model against both "
                        "parents' test datasets; both must pass the "
                        "promotion gate independently.")
    p.add_argument("--max-tokens", type=int, default=256,
                   help="Max tokens for specialist inference (default: 256).")
    p.add_argument("--gpu-memory-utilization", type=float, default=0.90,
                   help="vLLM GPU memory fraction (default: 0.90).")
    p.add_argument("--registry-root", default=None,
                   help="Override registry root (default: ~/.apprentice/registry).")
    p.add_argument("--failures-dir", default=None,
                   help="Override failures dir (default: ~/.apprentice/failures).")
    p.add_argument("--skip-skill-registration", action="store_true",
                   help="Don't render/stage/push a Hermes SKILL.md after promotion.")
    p.add_argument("--skill-staging-root", default=None,
                   help="Override skill staging root (default: ~/.apprentice/skills).")
    p.add_argument("--patterns-root", default=None,
                   help="Override patterns root (default: ~/.apprentice/patterns). "
                        "Used to read the pattern description for the SKILL.md.")
    p.add_argument("--pattern-description", default=None,
                   help="Override pattern description for the SKILL.md. Takes "
                        "precedence over <patterns-root>/<pattern-id>/manifest.json.")
    p.add_argument("--hermes-guest", default=skill_registrar.DEFAULT_GUEST_HOST,
                   help=f"SSH target for Hermes microVM (default: "
                        f"{skill_registrar.DEFAULT_GUEST_HOST}). Empty string skips "
                        f"the guest push and only stages locally.")
    p.add_argument("--hermes-guest-skills-dir",
                   default=skill_registrar.DEFAULT_GUEST_SKILLS_DIR,
                   help=f"Skills dir on the Hermes guest (default: "
                        f"{skill_registrar.DEFAULT_GUEST_SKILLS_DIR}).")
    p.add_argument("--check-only", action="store_true",
                   help="Validate args + dataset without running inference.")
    p.add_argument("--datasets-root", default=None,
                   help="Override datasets root for regression check (default: ~/.apprentice/datasets).")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def run_validate(args: argparse.Namespace) -> int:
    model_dir = Path(args.model_dir).expanduser().resolve()
    test_dataset = Path(args.test_dataset).expanduser().resolve()
    pattern_id = args.pattern_id

    if not args.baseline_pairs:
        LOG.error(
            "--baseline-pairs is required (run apprentice-baseline first). "
            "Use --check-only to validate args without running."
        )
        return 6
    baseline_pairs_path = Path(args.baseline_pairs).expanduser().resolve()

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

    # Load the cached baseline pairs and confirm they match our test set.
    # Loading the JSONL is much cheaper than the test set, so do it first.
    try:
        # Count test records first so we can sanity-check the pair count.
        from .test_runner import load_test_dataset
        test_records = load_test_dataset(test_dataset)
        baseline_header, baseline_pairs = baseline_io.read_pairs(
            path=baseline_pairs_path,
            expected_test_dataset=test_dataset,
            expected_count=len(test_records),
        )
    except (FileNotFoundError, ValueError) as e:
        LOG.error("baseline pairs load failed", extra={"error": str(e)})
        return 6

    LOG.info("validator starting", extra={
        "model_dir": str(model_dir),
        "test_dataset": str(test_dataset),
        "pattern_id": pattern_id,
        "baseline_pairs": str(baseline_pairs_path),
        "baseline_model": baseline_header.get("base_model"),
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

    # ---- baseline (loaded from cache) -------------------------------------
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

        if not args.skip_skill_registration:
            try:
                reg_result = skill_registrar.register_skill(
                    pattern_id=pattern_id,
                    description=args.pattern_description,
                    patterns_root=(
                        Path(args.patterns_root).expanduser().resolve()
                        if args.patterns_root else None
                    ),
                    staging_root=(
                        Path(args.skill_staging_root).expanduser().resolve()
                        if args.skill_staging_root else None
                    ),
                    guest_host=(args.hermes_guest or None),
                    guest_skills_dir=args.hermes_guest_skills_dir,
                )
                result["skill_registration"] = reg_result.as_dict()
            except (ValueError, OSError) as e:
                LOG.warning("skill registration failed (promotion stands)",
                            extra={"error": str(e)})
                result["skill_registration_error"] = str(e)
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

    # ---- merge regression check --------------------------------------------
    if args.merge_regression:
        reg_root = Path(args.datasets_root) if args.datasets_root else _apprentice_root() / "datasets"
        regression_results = _run_regression_check(
            model_dir=model_dir,
            parent_patterns=args.merge_regression,
            datasets_root=reg_root,
            max_tokens=args.max_tokens,
            gpu_memory_utilization=args.gpu_memory_utilization,
        )
        result["regression_check"] = regression_results
        regression_passed = all(r["passed"] for r in regression_results.values())
        if not regression_passed:
            LOG.warning("merge regression check failed on at least one parent")
            _print_result(result)
            return regression_results.get("exit_code", 1)

    _print_result(result)
    return 0 if verdict["passed"] else 1


def _resolve_latest_dataset(datasets_root: Path, pattern_id: str) -> Path | None:
    """Return the path to the latest versioned dataset for *pattern_id*."""
    pattern_dir = datasets_root / pattern_id
    if not pattern_dir.exists():
        return None
    max_v, max_dir = 0, None
    for entry in pattern_dir.iterdir():
        if entry.is_dir() and entry.name.startswith("v"):
            try:
                v = int(entry.name[1:])
                if v > max_v:
                    max_v, max_dir = v, entry
            except ValueError:
                continue
    return max_dir


def _run_regression_check(
    model_dir: Path,
    parent_patterns: list[str],
    datasets_root: Path,
    max_tokens: int,
    gpu_memory_utilization: float,
) -> dict[str, Any]:
    """Run inference on each parent's test set; both must pass the gate."""
    from . import baseline_io, metrics, promotion_gate, test_runner

    results: dict[str, Any] = {}
    all_passed = True

    for parent_id in parent_patterns:
        dataset_dir = _resolve_latest_dataset(datasets_root, parent_id)
        if not dataset_dir:
            results[parent_id] = {"error": f"no dataset found for {parent_id}", "passed": False}
            all_passed = False
            continue

        test_dataset = dataset_dir / "test.jsonl.gz"
        if not test_dataset.exists():
            results[parent_id] = {"error": f"test.jsonl.gz not found at {test_dataset}", "passed": False}
            all_passed = False
            continue

        # Resolve baseline pairs — same version as the dataset.
        version = dataset_dir.name
        baseline_path = _apprentice_root() / "baselines" / f"{parent_id}-{version}.jsonl"
        if not baseline_path.exists():
            results[parent_id] = {"error": f"baseline pairs not found at {baseline_path}", "passed": False}
            all_passed = False
            continue

        try:
            test_records = load_test_dataset(test_dataset)
            baseline_header, baseline_pairs = baseline_io.read_pairs(
                path=baseline_path,
                expected_test_dataset=test_dataset,
                expected_count=len(test_records),
            )
        except (FileNotFoundError, ValueError) as e:
            results[parent_id] = {"error": str(e), "passed": False}
            all_passed = False
            continue

        try:
            specialist_pairs = test_runner.run_specialist(
                model_dir=model_dir,
                test_dataset=test_dataset,
                max_tokens=max_tokens,
                gpu_memory_utilization=gpu_memory_utilization,
            )
        except RuntimeError as e:
            results[parent_id] = {"error": f"inference failed: {e}", "passed": False}
            all_passed = False
            continue

        specialist_scores = metrics.compute_metrics(specialist_pairs)
        baseline_scores = metrics.compute_metrics(baseline_pairs)
        comparison = metrics.compare_metrics(specialist_scores, baseline_scores)
        verdict = promotion_gate.evaluate(comparison)

        results[parent_id] = {
            "passed": verdict["passed"],
            "verdict": verdict,
            "specialist_scores": specialist_scores,
            "baseline_scores": baseline_scores,
        }
        if not verdict["passed"]:
            all_passed = False

    results["all_passed"] = all_passed
    results["exit_code"] = 0 if all_passed else 1
    return results


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
