"""Tests for test_runner module (CPU-only: dataset loading, check_only)."""

from __future__ import annotations

import gzip
import json
from pathlib import Path

import pytest

from apprentice_validator.test_runner import check_only, load_test_dataset


def _write_test_dataset(tmp_path: Path, records: list[dict]) -> Path:
    path = tmp_path / "test.jsonl.gz"
    with gzip.open(path, "wt", encoding="utf-8") as f:
        for r in records:
            f.write(json.dumps(r) + "\n")
    return path


def test_load_test_dataset_reads_records(tmp_path):
    records = [
        {"messages": [{"role": "user", "content": "hello"}]},
        {"messages": [{"role": "user", "content": "world"}]},
    ]
    path = _write_test_dataset(tmp_path, records)
    loaded = load_test_dataset(path)
    assert len(loaded) == 2
    assert loaded[0]["messages"][0]["content"] == "hello"


def test_load_test_dataset_file_not_found(tmp_path):
    with pytest.raises(FileNotFoundError, match="not found"):
        load_test_dataset(tmp_path / "nonexistent.jsonl.gz")


def test_load_test_dataset_empty_file_raises(tmp_path):
    path = _write_test_dataset(tmp_path, [])
    with pytest.raises(ValueError, match="empty"):
        load_test_dataset(path)


def test_load_test_dataset_skips_empty_lines(tmp_path):
    path = tmp_path / "test.jsonl.gz"
    records = [{"messages": [{"role": "user", "content": "a"}]}]
    content = "\n" + json.dumps(records[0]) + "\n\n"
    with gzip.open(path, "wt", encoding="utf-8") as f:
        f.write(content)
    loaded = load_test_dataset(path)
    assert len(loaded) == 1


def test_check_only_passes_on_valid_dataset(tmp_path):
    path = _write_test_dataset(tmp_path, [
        {"messages": [{"role": "user", "content": "test"}]},
    ])
    model_dir = tmp_path / "model"
    model_dir.mkdir()
    (model_dir / "config.json").write_text("{}")
    rc = check_only(test_dataset=path, model_dir=model_dir)
    assert rc == 0


def test_check_only_fails_on_missing_dataset(tmp_path):
    rc = check_only(
        test_dataset=tmp_path / "nonexistent.jsonl.gz",
        model_dir=tmp_path / "model",
    )
    assert rc != 0


def test_check_only_fails_on_missing_model(tmp_path):
    path = _write_test_dataset(tmp_path, [
        {"messages": [{"role": "user", "content": "test"}]},
    ])
    rc = check_only(test_dataset=path, model_dir=tmp_path / "nonexistent")
    assert rc != 0
