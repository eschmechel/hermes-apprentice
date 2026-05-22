"""Promotion gate (validator-04).

Implements the pass/fail decision:
    specialist >= baseline + 10pp  (exact-match AND F1)
    specialist >= teacher - 5pp    (F1 comparison, only when teacher_score is provided)

Returns a verdict dict suitable for registry signing or failure reporting.
"""

from __future__ import annotations

import logging
from typing import Any

LOG = logging.getLogger("apprentice_validator.gate")

MARGIN = 0.1   # required improvement (10 pp) over baseline on a 0-1 scale
TEACHER_MARGIN = 0.05  # allowed F1 shortfall (5 pp) below teacher on a 0-1 scale


def evaluate(
    comparison: dict[str, Any],
    teacher_score: float | None = None,
) -> dict[str, Any]:
    """Apply the promotion gate logic.

    Args:
        comparison: output of metrics.compare_metrics()
        teacher_score: optional teacher F1 score (0-1 scale) for comparison.
            When provided, the specialist's F1 must be >= teacher_score - 0.05.

    Returns:
        {
            "passed": bool,
            "reason": str,
            "comparison": <input comparison>,
            "margin": float (the baseline margin),
            "teacher_score": float | None,
            "teacher_passed": bool | None,
            "teacher_delta": float | None,
        }
    """
    delta_em = comparison.get("delta_exact_match", 0.0)
    delta_f1 = comparison.get("delta_f1", 0.0)

    em_ok = delta_em >= MARGIN
    f1_ok = delta_f1 >= MARGIN

    teacher_passed: bool | None = None
    teacher_delta: float | None = None
    if teacher_score is not None:
        specialist_f1 = comparison.get("specialist_f1", 0.0)
        teacher_delta = round(specialist_f1 - teacher_score, 6)
        teacher_passed = specialist_f1 >= teacher_score - TEACHER_MARGIN

    margin_pp = MARGIN * 100
    if em_ok and f1_ok and (teacher_passed is None or teacher_passed):
        passed = True
        parts = [
            f"specialist exceeds baseline by {delta_em * 100:.1f}pp exact-match "
            f"and {delta_f1 * 100:.1f}pp F1 (margin={margin_pp:.0f}pp)",
        ]
        if teacher_passed is not None:
            parts.append(
                f"specialist F1 {specialist_f1:.4f} within {TEACHER_MARGIN * 100:.0f}pp "
                f"of teacher {teacher_score:.4f}"
            )
        reason = "; ".join(parts)
        LOG.info("gate passed", extra={
            "delta_exact_match": delta_em,
            "delta_f1": delta_f1,
            "margin": MARGIN,
            "teacher_score": teacher_score,
            "teacher_passed": teacher_passed,
        })
    else:
        passed = False
        failures = []
        if not em_ok:
            failures.append(f"exact-match delta {delta_em * 100:.1f}pp < {margin_pp:.0f}pp")
        if not f1_ok:
            failures.append(f"F1 delta {delta_f1 * 100:.1f}pp < {margin_pp:.0f}pp")
        if teacher_passed is not None and not teacher_passed:
            failures.append(
                f"specialist F1 {comparison.get('specialist_f1', 0):.4f} "
                f"below teacher {teacher_score:.4f} minus "
                f"{TEACHER_MARGIN * 100:.0f}pp"
            )
        reason = "; ".join(failures)
        LOG.info("gate failed", extra={
            "delta_exact_match": delta_em,
            "delta_f1": delta_f1,
            "margin": MARGIN,
            "teacher_score": teacher_score,
            "teacher_passed": teacher_passed,
            "failures": failures,
        })

    verdict: dict[str, Any] = {
        "passed": passed,
        "reason": reason,
        "comparison": comparison,
        "margin": MARGIN,
        "teacher_score": teacher_score,
        "teacher_passed": teacher_passed,
        "teacher_delta": teacher_delta,
    }

    return verdict
