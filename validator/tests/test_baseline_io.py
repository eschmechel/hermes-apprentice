"""Tests for the apprentice-baseline ↔ apprentice-validate file seam.

The pairs file is the contract between the two CLIs; if the schema check or
the test-dataset cross-check breaks we silently corrupt the promotion gate.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from apprentice_validator import baseline_io


PAIRS_FIXTURE = [
    {"index": 0, "expected": "hello", "actual": "hi",      "expected_len": 5, "actual_len": 2},
    {"index": 1, "expected": "world", "actual": "earth",   "expected_len": 5, "actual_len": 5},
]


def test_roundtrip(tmp_path: Path):
    test_ds = tmp_path / "test.jsonl.gz"
    test_ds.write_bytes(b"\x1f\x8b")  # marker bytes — only the path needs to exist
    out = tmp_path / "baseline.jsonl"

    baseline_io.write_pairs(
        path=out,
        pairs=PAIRS_FIXTURE,
        test_dataset=test_ds,
        base_model="Qwen/Qwen2.5-1.5B-Instruct",
    )

    header, pairs = baseline_io.read_pairs(path=out)
    assert header["schema"] == baseline_io.SCHEMA
    assert header["version"] == baseline_io.SCHEMA_VERSION
    assert header["count"] == 2
    assert header["base_model"] == "Qwen/Qwen2.5-1.5B-Instruct"
    assert header["test_dataset"] == str(test_ds.resolve())
    assert pairs == PAIRS_FIXTURE


def test_header_must_be_first_line(tmp_path: Path):
    out = tmp_path / "bad.jsonl"
    out.write_text(json.dumps(PAIRS_FIXTURE[0]) + "\n")  # no header
    with pytest.raises(ValueError, match="not a 'apprentice-baseline-pairs' header"):
        baseline_io.read_pairs(path=out)


def test_empty_file_raises(tmp_path: Path):
    out = tmp_path / "empty.jsonl"
    out.write_text("")
    with pytest.raises(ValueError, match="empty"):
        baseline_io.read_pairs(path=out)


def test_missing_file_raises(tmp_path: Path):
    with pytest.raises(FileNotFoundError):
        baseline_io.read_pairs(path=tmp_path / "nope.jsonl")


def test_dataset_mismatch_raises(tmp_path: Path):
    test_ds = tmp_path / "real-test.jsonl.gz"
    test_ds.write_bytes(b"\x1f\x8b")
    other_ds = tmp_path / "different.jsonl.gz"
    other_ds.write_bytes(b"\x1f\x8b")
    out = tmp_path / "baseline.jsonl"

    baseline_io.write_pairs(
        path=out, pairs=PAIRS_FIXTURE, test_dataset=test_ds, base_model="x"
    )

    with pytest.raises(ValueError, match="Re-run apprentice-baseline"):
        baseline_io.read_pairs(
            path=out, expected_test_dataset=other_ds
        )


def test_count_mismatch_raises(tmp_path: Path):
    test_ds = tmp_path / "test.jsonl.gz"
    test_ds.write_bytes(b"\x1f\x8b")
    out = tmp_path / "baseline.jsonl"

    baseline_io.write_pairs(
        path=out, pairs=PAIRS_FIXTURE, test_dataset=test_ds, base_model="x"
    )
    # File has 2 pairs; we ask for 3 (test set grew).
    with pytest.raises(ValueError, match="2 records but test set has 3"):
        baseline_io.read_pairs(path=out, expected_count=3)


def test_write_atomic_via_tempfile(tmp_path: Path):
    """write_pairs uses a .tmp file + replace, so a crash mid-write doesn't
    leave a partial baseline.jsonl that read_pairs would silently consume."""
    test_ds = tmp_path / "test.jsonl.gz"
    test_ds.write_bytes(b"\x1f\x8b")
    out = tmp_path / "baseline.jsonl"

    baseline_io.write_pairs(
        path=out, pairs=PAIRS_FIXTURE, test_dataset=test_ds, base_model="x"
    )
    # After successful write: final file exists, no .tmp shadow.
    assert out.exists()
    assert not (tmp_path / "baseline.jsonl.tmp").exists()
