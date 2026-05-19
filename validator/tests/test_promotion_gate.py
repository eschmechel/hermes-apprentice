"""Tests for validator-04: promotion gate. CPU-only."""

from __future__ import annotations

import pytest

from apprentice_validator.promotion_gate import evaluate


def make_comparison(delta_em: float = 0.0, delta_f1: float = 0.0) -> dict:
    return {
        "specialist_exact_match": 0.40 + delta_em,
        "specialist_f1": 0.50 + delta_f1,
        "baseline_exact_match": 0.40,
        "baseline_f1": 0.50,
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


def test_teacher_score_is_recorded_but_not_used():
    verdict = evaluate(make_comparison(delta_em=0.15, delta_f1=0.15), teacher_score=85.0)
    assert verdict["passed"] is True
    assert verdict["teacher_score"] == 85.0


def test_verdict_contains_margin():
    verdict = evaluate(make_comparison(delta_em=0.20, delta_f1=0.20))
    assert verdict["margin"] == 0.1
