"""Tests for validator-03: metrics computation. CPU-only."""

from __future__ import annotations

import pytest

from apprentice_validator.metrics import (
    compare_metrics,
    compute_metrics,
    exact_match,
    tokenize,
)


def test_tokenize_basic():
    assert tokenize("Hello World") == ["hello", "world"]


def test_tokenize_handles_extra_whitespace():
    assert tokenize("  hello   world  ") == ["hello", "world"]


def test_tokenize_empty_string():
    assert tokenize("") == []


def test_exact_match_true():
    assert exact_match("hello world", "hello world") is True


def test_exact_match_case_insensitive():
    assert exact_match("Hello World", "hello world") is True


def test_exact_match_extra_whitespace():
    assert exact_match("  hello  world  ", "hello world") is True


def test_exact_match_false():
    assert exact_match("hello world", "goodbye world") is False


def test_compute_metrics_empty():
    result = compute_metrics([])
    assert result["exact_match"] == 0.0
    assert result["f1"] == 0.0
    assert result["count"] == 0


def test_compute_metrics_perfect():
    pairs = [
        {"expected": "hello world", "actual": "hello world"},
        {"expected": "foo bar", "actual": "foo bar"},
    ]
    result = compute_metrics(pairs)
    assert result["exact_match"] == 1.0
    assert result["f1"] == 1.0
    assert result["count"] == 2


def test_compute_metrics_partial():
    pairs = [
        {"expected": "hello world", "actual": "hello moon"},
        {"expected": "the quick brown fox", "actual": "the quick fox"},
    ]
    result = compute_metrics(pairs)
    assert result["exact_match"] < 1.0
    assert result["f1"] > 0.0
    assert result["count"] == 2


def test_compute_metrics_all_wrong():
    pairs = [
        {"expected": "hello", "actual": "goodbye"},
        {"expected": "foo", "actual": "bar"},
    ]
    result = compute_metrics(pairs)
    assert result["exact_match"] == 0.0
    assert result["f1"] == 0.0


def test_f1_uses_multiset_not_set():
    """Regression: F1 must use multiset intersection so repeated tokens
    on one side don't get a free pass against repeats on the other.

    expected: "cat cat dog"  → multiset {cat:2, dog:1} (sum=3)
    actual:   "cat dog dog"  → multiset {cat:1, dog:2} (sum=3)
    overlap multiset = {cat:1, dog:1} (sum=2)
    prec = rec = 2/3 → f1 = 2/3
    With the old set-based code, f1 would have been 1.0.
    """
    pairs = [{"expected": "cat cat dog", "actual": "cat dog dog"}]
    result = compute_metrics(pairs)
    assert 0.5 < result["f1"] < 0.8, f"expected ~0.667, got {result['f1']}"
    assert result["f1"] != 1.0


def test_compare_metrics():
    specialist = {"exact_match": 0.50, "f1": 0.65, "count": 100}
    baseline = {"exact_match": 0.30, "f1": 0.40, "count": 100}
    cmp = compare_metrics(specialist, baseline)
    assert cmp["delta_exact_match"] == 0.20
    assert cmp["delta_f1"] == 0.25


def test_compare_metrics_specialist_worse():
    specialist = {"exact_match": 0.20, "f1": 0.30, "count": 100}
    baseline = {"exact_match": 0.50, "f1": 0.60, "count": 100}
    cmp = compare_metrics(specialist, baseline)
    assert cmp["delta_exact_match"] == -0.30
    assert cmp["delta_f1"] == -0.30
