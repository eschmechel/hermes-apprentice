"""``apprentice-baseline`` CLI.

Runs the raw base model against a test set and caches the (expected, actual)
pairs to disk so ``apprentice-validate`` doesn't need to load the base model
itself.

Why this is its own command: a single consumer GPU (8–11 GB) cannot hold both
a specialist and the base model at the configured ``--gpu-memory-utilization
0.90`` simultaneously, so running them in the same Python process OOMs.
Splitting the workflow has a second benefit too — when you iterate the
specialist multiple times (re-train, re-validate), the baseline only needs to
be computed once per (dataset, base_model) pair.

Usage::

    apprentice-baseline \\
        --test-dataset ~/.apprentice/datasets/<pattern-id>/v1/test.jsonl.gz \\
        --output       ~/.apprentice/baselines/<pattern-id>-v1.jsonl \\
        [--baseline-model Qwen/Qwen2.5-1.5B-Instruct] \\
        [--max-tokens 256] \\
        [--gpu-memory-utilization 0.90] \\
        [--check-only]
"""

from __future__ import annotations

import argparse
import logging
import time
from pathlib import Path

from apprentice_trainer import models

from . import baseline_io, baseline_runner, test_runner
from .logging import setup_logging

LOG = logging.getLogger("apprentice_validator.baseline_cli")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-baseline",
        description="Run the raw base model against a test set and cache "
                    "(expected, actual) pairs for apprentice-validate.",
    )
    p.add_argument("--test-dataset", required=True,
                   help="Path to test.jsonl.gz (from dataset-builder splitter).")
    p.add_argument("--output", required=True,
                   help="Where to write the baseline pairs JSONL.")
    p.add_argument("--baseline-model", default=None,
                   help="HF model id (default: the entry marked default: true in "
                        "supported_models.yaml). Use --list-models to see available models.")
    p.add_argument("--list-models", action="store_true",
                   help="Print available base models and exit.")
    p.add_argument("--max-tokens", type=int, default=256,
                   help="Max tokens for inference (default: 256).")
    p.add_argument("--gpu-memory-utilization", type=float, default=0.90,
                   help="vLLM GPU memory fraction (default: 0.90).")
    p.add_argument("--check-only", action="store_true",
                   help="Validate test dataset structure without running inference.")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    setup_logging(logging.DEBUG if args.verbose else logging.INFO)

    test_dataset = Path(args.test_dataset).expanduser().resolve()
    output = Path(args.output).expanduser().resolve()

    if args.list_models:
        for m in models.list_models():
            default_mark = " (default)" if m.get("default") else ""
            print(f"  {m['id']}{default_mark}")
        return 0

    resolved_base = models.resolve_model(args.baseline_model, load_in_4bit=False)
    args.baseline_model = resolved_base

    if args.check_only:
        try:
            records = test_runner.load_test_dataset(test_dataset)
        except (FileNotFoundError, ValueError) as e:
            LOG.error("check-only failed", extra={"error": str(e)})
            return 5
        LOG.info("check-only passed", extra={
            "test_dataset": str(test_dataset),
            "test_count": len(records),
            "output": str(output),
        })
        return 0

    LOG.info("apprentice-baseline starting", extra={
        "test_dataset": str(test_dataset),
        "output": str(output),
        "baseline_model": args.baseline_model,
        "max_tokens": args.max_tokens,
    })

    t0 = time.time()
    try:
        pairs = baseline_runner.run_baseline(
            test_dataset=test_dataset,
            base_model=args.baseline_model,
            max_tokens=args.max_tokens,
            gpu_memory_utilization=args.gpu_memory_utilization,
        )
    except RuntimeError as e:
        LOG.error("baseline inference failed", extra={"error": str(e)})
        return 3

    baseline_io.write_pairs(
        path=output,
        pairs=pairs,
        test_dataset=test_dataset,
        base_model=args.baseline_model,
    )

    elapsed = time.time() - t0
    LOG.info("apprentice-baseline complete", extra={
        "output": str(output),
        "pairs": len(pairs),
        "wallclock_seconds": round(elapsed, 1),
    })
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
