"""Lightweight tests for trainer's CPU-friendly paths.

The Unsloth/torch training path needs CUDA; we don't test it here. We test
argument parsing, JSON-gz dataset loading, the check-only validator, and the
JSON log formatter so a CI without GPUs can still catch regressions in
scaffolding.
"""

from __future__ import annotations

import gzip
import json
import logging
import sys
import textwrap
from pathlib import Path

import pytest

from apprentice_trainer import train as t


def _write_jsonl_gz(path: Path, rows: list[dict]) -> None:
    with gzip.open(path, "wt", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")


@pytest.fixture
def dataset_dir(tmp_path: Path) -> Path:
    rows_train = [
        {"messages": [
            {"role": "system", "content": "You are Hermes."},
            {"role": "user", "content": f"prompt {i}"},
            {"role": "assistant", "content": f"answer {i}"},
        ]} for i in range(4)
    ]
    rows_val = rows_train[:1]
    _write_jsonl_gz(tmp_path / "train.jsonl.gz", rows_train)
    _write_jsonl_gz(tmp_path / "val.jsonl.gz", rows_val)
    return tmp_path


def test_load_dataset_returns_train_and_val(dataset_dir: Path):
    train, val = t.load_dataset_jsonl_gz(dataset_dir)
    assert len(train) == 4
    assert len(val) == 1
    assert train[0]["messages"][0]["role"] == "system"


def test_load_dataset_val_optional(tmp_path: Path):
    _write_jsonl_gz(tmp_path / "train.jsonl.gz", [{"messages": [{"role": "user", "content": "hi"}]}])
    train, val = t.load_dataset_jsonl_gz(tmp_path)
    assert len(train) == 1
    assert val == []


def test_load_dataset_missing_train_raises(tmp_path: Path):
    with pytest.raises(FileNotFoundError):
        t.load_dataset_jsonl_gz(tmp_path)


def test_load_dataset_bad_json_surfaces_line_number(tmp_path: Path):
    p = tmp_path / "train.jsonl.gz"
    with gzip.open(p, "wt") as f:
        f.write('{"messages": [{"role": "user"}]}\n')
        f.write("this is not json\n")
    with pytest.raises(ValueError) as exc:
        t.load_dataset_jsonl_gz(tmp_path)
    assert ":2:" in str(exc.value)


def test_check_only_exit_code_zero(dataset_dir: Path, tmp_path: Path):
    rc = t.main([
        "--dataset-dir", str(dataset_dir),
        "--output-dir", str(tmp_path / "out"),
        "--check-only",
    ])
    assert rc == 0


def test_check_only_flags_malformed_rows(tmp_path: Path):
    _write_jsonl_gz(tmp_path / "train.jsonl.gz", [{"not_messages": True}])
    rc = t.main([
        "--dataset-dir", str(tmp_path),
        "--output-dir", str(tmp_path / "out"),
        "--check-only",
    ])
    assert rc == 5


def test_parser_defaults_match_acceptance():
    args = t.build_parser().parse_args([
        "--dataset-dir", "/tmp/ds",
        "--output-dir", "/tmp/out",
    ])
    # Acceptance: LoRA rank 16, base model Qwen2.5-1.5B-Instruct.
    assert args.lora_rank == 16
    assert "Qwen2.5-1.5B-Instruct" in args.base_model
    assert args.max_seq_len == 2048


def test_json_formatter_includes_extras_and_skips_builtin_noise(caplog):
    fmt = t._JSONFormatter()
    rec = logging.LogRecord(
        name="apprentice_trainer", level=logging.INFO, pathname=__file__, lineno=1,
        msg="hello %s", args=("world",), exc_info=None,
    )
    rec.dataset_dir = "/foo"      # caller-supplied via extra=
    rec.taskName = "asyncio-1"    # Python 3.12+ noise that must NOT appear
    out = json.loads(fmt.format(rec))
    assert out["msg"] == "hello world"
    assert out["dataset_dir"] == "/foo"
    assert "taskName" not in out
    assert out["component"] == "apprentice_trainer"
