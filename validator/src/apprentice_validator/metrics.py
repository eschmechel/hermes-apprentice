"""Metrics computer (validator-03).

Computes exact-match ratio and token-level F1 score for a list of
(expected, actual) text pairs.  Tokenisation is simple whitespace splitting
so the module is self-contained and doesn't depend on a specific tokenizer.
"""

from __future__ import annotations

import logging
from typing import Any

LOG = logging.getLogger("apprentice_validator.metrics")

Pair = dict[str, Any]
ScoreDict = dict[str, float]


def tokenize(text: str) -> list[str]:
    """Split on whitespace, lowercase, strip empty tokens.

    Deliberately simple so the module has no external tokenizer dependency.
    Real token-level metrics (e.g. BERTScore) would need a model, but
    whitespace-split F1 is fast, deterministic, and good enough for the
    promotion gate's ordinal comparison ("is the specialist better than
    baseline by margin X?").
    """
    return [t.lower() for t in text.split() if t.strip()]


def exact_match(expected: str, actual: str) -> bool:
    """Case-insensitive whitespace-normalised exact match."""
    return tokenize(expected) == tokenize(actual)


def _precision_recall_f1(expected_toks: list[str], actual_toks: list[str]) -> tuple[float, float, float]:
    """Return (precision, recall, f1) for token sets."""
    exp_set = set(expected_toks)
    act_set = set(actual_toks)
    if not exp_set and not act_set:
        return 1.0, 1.0, 1.0
    if not exp_set or not act_set:
        return 0.0, 0.0, 0.0
    intersection = exp_set & act_set
    prec = len(intersection) / len(act_set)
    rec = len(intersection) / len(exp_set)
    if prec + rec == 0:
        return 0.0, 0.0, 0.0
    f1 = 2 * prec * rec / (prec + rec)
    return prec, rec, f1


def compute_metrics(pairs: list[Pair]) -> ScoreDict:
    """Compute aggregate scores from a list of (expected, actual) pairs.

    Returns:
        {"exact_match": 0.0–1.0, "f1": 0.0–1.0, "count": int, "pairwise": [...]}
    where `pairwise` is lifted from the incoming pairs for traceability.
    """
    if not pairs:
        return {"exact_match": 0.0, "f1": 0.0, "count": 0, "pairwise": []}

    em_hits = 0
    f1_sum = 0.0
    enriched: list[dict[str, Any]] = []

    for p in pairs:
        expected = p.get("expected", "")
        actual = p.get("actual", "")
        is_em = exact_match(expected, actual)
        _, _, f1_val = _precision_recall_f1(tokenize(expected), tokenize(actual))
        if is_em:
            em_hits += 1
        f1_sum += f1_val
        enriched.append({**p, "exact_match": is_em, "f1": round(f1_val, 4)})

    n = len(pairs)
    result: ScoreDict = {
        "exact_match": round(em_hits / n, 4),
        "f1": round(f1_sum / n, 4),
        "count": n,
        "pairwise": enriched,
    }

    LOG.info("metrics computed", extra={
        "count": n,
        "exact_match": result["exact_match"],
        "f1": result["f1"],
    })
    return result


def compare_metrics(specialist: ScoreDict, baseline: ScoreDict) -> ScoreDict:
    """Return a comparison dict: specialist scores, baseline scores, deltas."""
    return {
        "specialist_exact_match": specialist.get("exact_match", 0.0),
        "specialist_f1": specialist.get("f1", 0.0),
        "baseline_exact_match": baseline.get("exact_match", 0.0),
        "baseline_f1": baseline.get("f1", 0.0),
        "delta_exact_match": round(
            specialist.get("exact_match", 0.0) - baseline.get("exact_match", 0.0), 4
        ),
        "delta_f1": round(
            specialist.get("f1", 0.0) - baseline.get("f1", 0.0), 4
        ),
        "count": specialist.get("count", 0),
    }
