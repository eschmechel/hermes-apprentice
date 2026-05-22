"""Tests for validator-04: promotion gate. CPU-only."""

from __future__ import annotations

import pytest

from apprentice_validator.promotion_gate import evaluate


def make_comparison(
    delta_em: float = 0.0,
    delta_f1: float = 0.0,
    specialist_f1: float | None = None,
) -> dict:
    base_em = 0.40
    base_f1 = 0.50
    sf1 = specialist_f1 if specialist_f1 is not None else base_f1 + delta_f1
    return {
        "specialist_exact_match": base_em + delta_em,
        "specialist_f1": sf1,
        "baseline_exact_match": base_em,
        "baseline_f1": base_f1,
        "delta_exact_match": delta_em,
        "delta_f1": delta_f1,
        "count": 100,
    }


def test_pass_when_both_deltas_exceed_margin():
    verdict = evaluate(make_comparison(delta_em=0.15, delta_f1=0.12))
    assert verdict["passed"] is True


def test_pass_at_exact_margin():
    verdict = evaluate(make_comparison(delta_em=0.10, delta_f1=0.10))
    assert verdict["passed"] is True


def test_fail_when_em_below_margin():
    verdict = evaluate(make_comparison(delta_em=0.09, delta_f1=0.15))
    assert verdict["passed"] is False
    assert "exact-match" in verdict["reason"].lower()


def test_fail_when_f1_below_margin():
    verdict = evaluate(make_comparison(delta_em=0.15, delta_f1=0.05))
    assert verdict["passed"] is False
    assert "f1" in verdict["reason"].lower()


def test_fail_when_both_below_margin():
    verdict = evaluate(make_comparison(delta_em=0.03, delta_f1=0.04))
    assert verdict["passed"] is False


def test_fail_on_negative_deltas():
    verdict = evaluate(make_comparison(delta_em=-0.05, delta_f1=-0.10))
    assert verdict["passed"] is False


def test_teacher_passed_when_specialist_within_margin():
    """specialist F1=0.85, teacher=0.90 -> delta=-0.05 -> still >= -0.05 -> pass."""
    verdict = evaluate(
        make_comparison(delta_em=0.15, delta_f1=0.15, specialist_f1=0.85),
        teacher_score=0.90,
    )
    assert verdict["passed"] is True
    assert verdict["teacher_passed"] is True
    assert verdict["teacher_delta"] == -0.05


def test_teacher_beats_teacher():
    """specialist F1=0.95, teacher=0.90 -> specialist exceeds teacher."""
    verdict = evaluate(
        make_comparison(delta_em=0.15, delta_f1=0.15, specialist_f1=0.95),
        teacher_score=0.90,
    )
    assert verdict["passed"] is True
    assert verdict["teacher_passed"] is True
    assert verdict["teacher_delta"] > 0


def test_teacher_fails_when_specialist_too_low():
    """specialist F1=0.80, teacher=0.90 -> delta=-0.10 < -0.05 -> fail."""
    verdict = evaluate(
        make_comparison(delta_em=0.15, delta_f1=0.15, specialist_f1=0.80),
        teacher_score=0.90,
    )
    assert verdict["passed"] is False
    assert verdict["teacher_passed"] is False
    assert verdict["teacher_delta"] == -0.10
    assert "teacher" in verdict["reason"].lower()


def test_teacher_optional_none_still_works():
    """When teacher_score is None, teacher fields are None but gate still works."""
    verdict = evaluate(make_comparison(delta_em=0.15, delta_f1=0.15))
    assert verdict["passed"] is True
    assert verdict["teacher_score"] is None
    assert verdict["teacher_passed"] is None
    assert verdict["teacher_delta"] is None


def test_verdict_contains_margin():
    verdict = evaluate(make_comparison(delta_em=0.20, delta_f1=0.20))
    assert verdict["margin"] == 0.1


def test_pass_reason_uses_percentage_points():
    """Reason string should show deltas in percentage points (multiplied by 100).
    Regression: previously emitted ``"0.2pp"`` for a 0.20 fraction.
    """
    verdict = evaluate(make_comparison(delta_em=0.20, delta_f1=0.25))
    assert "20.0pp" in verdict["reason"]
    assert "25.0pp" in verdict["reason"]
    assert "margin=10pp" in verdict["reason"]


def test_fail_reason_uses_percentage_points():
    verdict = evaluate(make_comparison(delta_em=0.05, delta_f1=0.03))
    assert "5.0pp" in verdict["reason"]
    assert "3.0pp" in verdict["reason"]
    assert "10pp" in verdict["reason"]
