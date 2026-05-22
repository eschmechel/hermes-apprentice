"""Tests for merge regression check logic (Phase 2c.4). CPU-only.

These tests verify the regression check helpers without running actual
inference (which requires a GPU). The helpers that parse dataset paths
and resolve versions are tested directly.
"""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from apprentice_validator.cli import _resolve_latest_dataset, _run_regression_check


@pytest.fixture
def datasets_root(tmp_path: Path) -> Path:
    """Create a mock datasets directory with versioned datasets."""
    root = tmp_path / "datasets"
    root.mkdir()
    return root


def _create_dataset_version(base: Path, pattern_id: str, version: int, count: int = 10):
    """Create a versioned dataset with test.jsonl.gz."""
    vdir = base / pattern_id / f"v{version}"
    vdir.mkdir(parents=True)
    # Write a minimal test.jsonl.gz (needs to be real gzip for load_test_dataset).
    import gzip
    records = [
        '{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"world"}]}'
        for _ in range(count)
    ]
    with gzip.open(str(vdir / "test.jsonl.gz"), "wt", encoding="utf-8") as f:
        for r in records:
            f.write(r + "\n")
    return vdir


def test_resolve_latest_dataset_returns_newest(datasets_root: Path):
    _create_dataset_version(datasets_root, "p1", 1)
    _create_dataset_version(datasets_root, "p1", 3)
    _create_dataset_version(datasets_root, "p1", 2)

    latest = _resolve_latest_dataset(datasets_root, "p1")
    assert latest is not None
    assert latest.name == "v3"


def test_resolve_latest_dataset_returns_none_for_missing(datasets_root: Path):
    latest = _resolve_latest_dataset(datasets_root, "nonexistent")
    assert latest is None


def test_resolve_latest_dataset_returns_none_for_empty_dir(datasets_root: Path):
    (datasets_root / "empty").mkdir()
    latest = _resolve_latest_dataset(datasets_root, "empty")
    assert latest is None


def test_resolve_latest_dataset_ignores_non_version_dirs(datasets_root: Path):
    pdir = datasets_root / "p1"
    pdir.mkdir()
    (pdir / "manifest.json").write_text("{}")
    (pdir / "foo").mkdir()
    _create_dataset_version(pdir, "", 2)  # note: pattern dir already exists

    latest = _resolve_latest_dataset(datasets_root, "p1")
    assert latest is not None
    assert latest.name == "v2"


def test_regression_check_returns_errors_for_missing_dataset(datasets_root: Path):
    result = _run_regression_check(
        model_dir=Path("/fake/model"),
        parent_patterns=["missing-pattern"],
        datasets_root=datasets_root,
        max_tokens=256,
        gpu_memory_utilization=0.90,
    )
    assert "missing-pattern" in result
    assert result["missing-pattern"]["passed"] is False
    assert "no dataset found" in result["missing-pattern"]["error"]
    assert result["all_passed"] is False


def test_regression_check_returns_errors_for_missing_baseline(datasets_root: Path, tmp_path):
    _create_dataset_version(datasets_root, "p1", 1)
    result = _run_regression_check(
        model_dir=Path("/fake/model"),
        parent_patterns=["p1"],
        datasets_root=datasets_root,
        max_tokens=256,
        gpu_memory_utilization=0.90,
    )
    assert "p1" in result
    assert result["p1"]["passed"] is False
    assert "baseline pairs not found" in result["p1"]["error"]
    assert result["all_passed"] is False


def test_regression_check_reports_error_for_nonexistent_test_file(datasets_root: Path):
    """Regression check should report error if test.jsonl.gz is missing."""
    pdir = datasets_root / "p1"
    pdir.mkdir()
    (pdir / "v1").mkdir()  # no test.jsonl.gz

    result = _run_regression_check(
        model_dir=Path("/fake/model"),
        parent_patterns=["p1"],
        datasets_root=datasets_root,
        max_tokens=256,
        gpu_memory_utilization=0.90,
    )
    assert result["p1"]["passed"] is False
    assert "test.jsonl.gz not found" in result["p1"]["error"]
