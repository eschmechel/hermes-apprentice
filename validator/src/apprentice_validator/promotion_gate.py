"""Promotion gate (validator-04).

Implements the pass/fail decision:
    specialist >= baseline + 10  (exact-match AND F1)

If an optional teacher_score is provided, it is also reported but does not
change the verdict (the dataset-builder augmentation metadata doesn't carry
per-example teacher outputs, so a hard teacher comparison is deferred until
the pipeline can produce that data consistently).

Returns a verdict dict suitable for registry signing or failure reporting.
"""

from __future__ import annotations

import logging
from typing import Any

LOG = logging.getLogger("apprentice_validator.gate")

MARGIN = 0.1  # required improvement (10 pp) over baseline on a 0-1 scale


def evaluate(
    comparison: dict[str, Any],
    teacher_score: float | None = None,
) -> dict[str, Any]:
    """Apply the promotion gate logic.

    Args:
        comparison: output of metrics.compare_metrics()
        teacher_score: optional teacher F1 score for reporting only

    Returns:
        {
            "passed": bool,
            "reason": str,
            "comparison": <input comparison>,
            "margin": int (the required margin),
            "teacher_score": float | None,
        }
    """
    delta_em = comparison.get("delta_exact_match", 0.0)
    delta_f1 = comparison.get("delta_f1", 0.0)

    em_ok = delta_em >= MARGIN
    f1_ok = delta_f1 >= MARGIN

    margin_pp = MARGIN * 100
    if em_ok and f1_ok:
        passed = True
        reason = (
            f"specialist exceeds baseline by {delta_em * 100:.1f}pp exact-match "
            f"and {delta_f1 * 100:.1f}pp F1 (margin={margin_pp:.0f}pp)"
        )
        LOG.info("gate passed", extra={
            "delta_exact_match": delta_em,
            "delta_f1": delta_f1,
            "margin": MARGIN,
        })
    else:
        passed = False
        failures = []
        if not em_ok:
            failures.append(f"exact-match delta {delta_em * 100:.1f}pp < {margin_pp:.0f}pp")
        if not f1_ok:
            failures.append(f"F1 delta {delta_f1 * 100:.1f}pp < {margin_pp:.0f}pp")
        reason = "; ".join(failures)
        LOG.info("gate failed", extra={
            "delta_exact_match": delta_em,
            "delta_f1": delta_f1,
            "margin": MARGIN,
            "failures": failures,
        })

    verdict: dict[str, Any] = {
        "passed": passed,
        "reason": reason,
        "comparison": comparison,
        "margin": MARGIN,
        "teacher_score": teacher_score,
    }

    if teacher_score is not None:
        LOG.warning(
            "teacher_score provided but not used in gate — "
            "dataset-builder augmentation does not persist per-example "
            "teacher outputs; teacher comparison is deferred",
            extra={"teacher_score": teacher_score},
        )

    return verdict
