"""Tests for validator-06: failure reporter. CPU-only."""

from __future__ import annotations

import json
from pathlib import Path

from apprentice_validator import failure_reporter


def test_report_structure(tmp_path: Path):
    failures_dir = tmp_path / "failures"
    verdict = {
        "passed": False,
        "reason": "F1 delta 3.5 < 10",
        "comparison": {
            "delta_exact_match": 12.0,
            "delta_f1": 3.5,
            "specialist_exact_match": 0.40,
            "specialist_f1": 0.35,
            "baseline_exact_match": 0.28,
            "baseline_f1": 0.315,
        },
        "margin": 10,
        "teacher_score": None,
    }
    path = failure_reporter.report(
        pattern_id="test-pattern",
        model_dir=Path("/fake/model"),
        test_dataset=Path("/fake/test.jsonl.gz"),
        verdict=verdict,
        failures_dir=failures_dir,
    )
    assert path.exists()
    assert path.suffix == ".json"
    assert "test-pattern" in path.name

    data = json.loads(path.read_text())
    assert data["pattern_id"] == "test-pattern"
    assert data["model_dir"] == "/fake/model"
    assert data["test_dataset"] == "/fake/test.jsonl.gz"
    assert data["verdict"]["passed"] is False
    assert "run_id" in data
    assert "timestamp" in data
    assert "suggested_action" in data


def test_report_suggests_f1_when_only_f1_bad(tmp_path: Path):
    verdict = {
        "passed": False,
        "reason": "F1 delta 0.05 < 0.1",
        "comparison": {"delta_exact_match": 0.15, "delta_f1": 0.05},
        "margin": 0.1,
        "teacher_score": None,
    }
    path = failure_reporter.report(
        pattern_id="p",
        model_dir=Path("/m"),
        test_dataset=Path("/t"),
        verdict=verdict,
        failures_dir=tmp_path,
    )
    data = json.loads(path.read_text())
    assert "f1" in data["suggested_action"].lower()


def test_report_suggests_em_when_only_em_bad(tmp_path: Path):
    verdict = {
        "passed": False,
        "reason": "exact-match delta 0.03 < 0.1",
        "comparison": {"delta_exact_match": 0.03, "delta_f1": 0.20},
        "margin": 0.1,
        "teacher_score": None,
    }
    path = failure_reporter.report(
        pattern_id="p",
        model_dir=Path("/m"),
        test_dataset=Path("/t"),
        verdict=verdict,
        failures_dir=tmp_path,
    )
    data = json.loads(path.read_text())
    assert "exact" in data["suggested_action"].lower() or "match" in data["suggested_action"].lower()
