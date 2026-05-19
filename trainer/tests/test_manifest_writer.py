"""Tests for trainer-06's manifest writer. CPU-only."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from apprentice_trainer import manifest_writer as mw


def test_read_dataset_hash_present(tmp_path: Path):
    (tmp_path / "manifest.json").write_text(json.dumps({"sha256": "abcd1234"}))
    assert mw.read_dataset_hash(tmp_path) == "abcd1234"


def test_read_dataset_hash_missing_file_returns_none(tmp_path: Path):
    assert mw.read_dataset_hash(tmp_path) is None


def test_read_dataset_hash_malformed_json_returns_none(tmp_path: Path):
    (tmp_path / "manifest.json").write_text("not json")
    assert mw.read_dataset_hash(tmp_path) is None


def test_read_dataset_hash_missing_sha_field_returns_none(tmp_path: Path):
    (tmp_path / "manifest.json").write_text(json.dumps({"version": 1}))
    assert mw.read_dataset_hash(tmp_path) is None


def test_build_manifest_contains_required_fields(tmp_path: Path):
    (tmp_path / "manifest.json").write_text(json.dumps({"sha256": "deadbeef"}))
    m = mw.build_manifest(
        dataset_dir=tmp_path,
        base_model="unsloth/Qwen2.5-1.5B-Instruct",
        hyperparameters={"lora_rank": 16, "batch_size": 2, "max_steps": 60},
        runtime_seconds=42.123456,
        exit_code=0,
    )
    # acceptance: contains dataset_hash, base_model, hyperparameters,
    # runtime_seconds, exit_code
    assert m["dataset_hash"] == "deadbeef"
    assert m["base_model"] == "unsloth/Qwen2.5-1.5B-Instruct"
    assert m["hyperparameters"]["lora_rank"] == 16
    assert m["runtime_seconds"] == 42.123
    assert m["exit_code"] == 0
    # bonus: created_at is ISO-8601 UTC
    assert m["created_at"].endswith("Z")


def test_write_manifest_is_valid_json_and_round_trips(tmp_path: Path):
    out = tmp_path / "out"
    m = mw.build_manifest(
        dataset_dir=tmp_path,
        base_model="m",
        hyperparameters={"x": 1},
        runtime_seconds=1.0,
        exit_code=0,
    )
    written = mw.write_manifest(out, m)
    # acceptance: manifest is valid JSON
    parsed = json.loads(written.read_text(encoding="utf-8"))
    assert parsed["base_model"] == "m"
    assert parsed["exit_code"] == 0


def test_write_manifest_is_deterministic_for_same_input(tmp_path: Path):
    """sort_keys + indent=2 must produce identical bytes for identical input
    so trainer-07's Ed25519 signature is reproducible."""
    m = mw.build_manifest(
        dataset_dir=tmp_path, base_model="m",
        hyperparameters={"b": 2, "a": 1, "c": 3},  # purposely unordered
        runtime_seconds=1.0, exit_code=0,
    )
    out1 = tmp_path / "out1"
    out2 = tmp_path / "out2"
    p1 = mw.write_manifest(out1, m)
    p2 = mw.write_manifest(out2, m)
    assert p1.read_bytes() == p2.read_bytes()


def test_write_manifest_atomic_tmpfile_not_left_behind(tmp_path: Path):
    out = tmp_path / "out"
    m = mw.build_manifest(
        dataset_dir=tmp_path, base_model="m", hyperparameters={},
        runtime_seconds=0.0, exit_code=0,
    )
    mw.write_manifest(out, m)
    leftover = list(out.glob("*.tmp"))
    assert leftover == [], f"atomic write left temp file: {leftover}"


def test_collect_hyperparameters_allow_list(tmp_path: Path):
    class FakeArgs:
        base_model = "m"
        load_in_4bit = True
        max_seq_len = 2048
        lora_rank = 16
        max_steps = 60
        batch_size = 2
        grad_accum = 4
        learning_rate = 2e-4
        warmup_steps = 5
        seed = 3407
        # noise that must NOT propagate:
        verbose = True
        check_only = False
        dataset_dir = "/foo"
        output_dir = "/bar"
        profile = None
    hp = mw.collect_hyperparameters(FakeArgs())
    assert "verbose" not in hp
    assert "dataset_dir" not in hp
    assert "profile" not in hp
    assert hp["lora_rank"] == 16
    assert hp["load_in_4bit"] is True


def test_failure_manifest_records_nonzero_exit(tmp_path: Path):
    m = mw.build_manifest(
        dataset_dir=tmp_path, base_model="m", hyperparameters={},
        runtime_seconds=12.0, exit_code=137,  # OOM
    )
    assert m["exit_code"] == 137
