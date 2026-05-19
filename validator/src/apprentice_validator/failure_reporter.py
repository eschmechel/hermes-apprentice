"""Failure reporter (validator-06).

When a model fails the promotion gate, writes a structured failure report to
``~/apprentice/failures/<run-id>.json`` with scores, dataset info, and
suggested remediation steps.
"""

from __future__ import annotations

import datetime
import json
import logging
from pathlib import Path
from typing import Any

from . import promotion_gate

LOG = logging.getLogger("apprentice_validator.failure")

DEFAULT_FAILURES_DIR = Path.home() / "apprentice" / "failures"

SUGGESTIONS: dict[str, str] = {
    "f1": (
        "F1 score below threshold. Try: (1) increase max-steps (60→200), "
        "(2) lower learning-rate (2e-4→5e-5), (3) check that the dataset "
        "has enough diverse examples (>20 recommended, ideally 50+)."
    ),
    "exact_match": (
        "Exact-match score below threshold. Try: (1) increase max-tokens "
        "to allow longer completions, (2) check that test examples are "
        "answerable from the provided context, (3) reduce temperature to 0 "
        "for deterministic output."
    ),
    "default": (
        "Model did not meet the promotion threshold. Review the dataset "
        "quality (check for PII leaks, dedup issues, or poor augmentations), "
        "increase training steps, or try a larger LoRA rank (16→32)."
    ),
}


def report(
    *,
    pattern_id: str,
    model_dir: Path,
    test_dataset: Path,
    verdict: dict[str, Any],
    failures_dir: Path | None = None,
) -> Path:
    """Write a structured failure report and return its path."""
    failures_dir = Path(failures_dir) if failures_dir else DEFAULT_FAILURES_DIR
    failures_dir.mkdir(parents=True, exist_ok=True)

    ts = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
    safe_ts = ts.replace(":", "-")
    run_id = f"{pattern_id}-{safe_ts}"
    report_path = failures_dir / f"{run_id}.json"

    comparison = verdict.get("comparison", {})
    delta_em = comparison.get("delta_exact_match", 0.0)
    delta_f1 = comparison.get("delta_f1", 0.0)

    if delta_f1 < promotion_gate.MARGIN and delta_em >= promotion_gate.MARGIN:
        suggestion = SUGGESTIONS["f1"]
    elif delta_em < promotion_gate.MARGIN and delta_f1 >= promotion_gate.MARGIN:
        suggestion = SUGGESTIONS["exact_match"]
    else:
        suggestion = SUGGESTIONS["default"]

    report_doc = {
        "run_id": run_id,
        "timestamp": ts,
        "pattern_id": pattern_id,
        "model_dir": str(model_dir.resolve()),
        "test_dataset": str(test_dataset.resolve()),
        "verdict": verdict,
        "suggested_action": suggestion,
    }

    payload = json.dumps(report_doc, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    report_path.write_text(payload, encoding="utf-8")

    LOG.info("failure report written", extra={
        "run_id": run_id,
        "path": str(report_path),
        "delta_exact_match": delta_em,
        "delta_f1": delta_f1,
    })
    return report_path
