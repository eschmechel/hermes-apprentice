"""Tests for baseline_cli module (argparse shape, check-only)."""

from __future__ import annotations

import pytest

from apprentice_validator import baseline_cli


def test_build_parser_requires_test_dataset():
    p = baseline_cli.build_parser()
    with pytest.raises(SystemExit):
        p.parse_args([])


def test_build_parser_requires_output():
    p = baseline_cli.build_parser()
    with pytest.raises(SystemExit):
        p.parse_args(["--test-dataset", "/tmp/test.jsonl.gz"])


def test_build_parser_accepts_minimal_args():
    p = baseline_cli.build_parser()
    args = p.parse_args([
        "--test-dataset", "/tmp/test.jsonl.gz",
        "--output", "/tmp/baseline.jsonl",
    ])
    assert args.test_dataset == "/tmp/test.jsonl.gz"
    assert args.output == "/tmp/baseline.jsonl"
    assert args.check_only is False


def test_build_parser_check_only():
    p = baseline_cli.build_parser()
    args = p.parse_args([
        "--test-dataset", "/tmp/test.jsonl.gz",
        "--output", "/tmp/baseline.jsonl",
        "--check-only",
    ])
    assert args.check_only is True


def test_build_parser_baseline_model_default():
    p = baseline_cli.build_parser()
    args = p.parse_args([
        "--test-dataset", "/tmp/a.jsonl.gz",
        "--output", "/tmp/b.jsonl",
    ])
    # Default is None (resolved at runtime from supported_models.yaml).
    assert args.baseline_model is None


def test_build_parser_max_tokens_default():
    p = baseline_cli.build_parser()
    args = p.parse_args([
        "--test-dataset", "/tmp/a.jsonl.gz",
        "--output", "/tmp/b.jsonl",
    ])
    assert args.max_tokens == 256


def test_check_only_passes_with_valid_dataset(tmp_path):
    """CPU-only: write a minimal gzip test dataset, run baseline check-only."""
    import gzip
    import json

    dataset = tmp_path / "test.jsonl.gz"
    with gzip.open(dataset, "wt", encoding="utf-8") as f:
        f.write(json.dumps({"messages": [{"role": "user", "content": "hello"}]}) + "\n")

    output = tmp_path / "baseline.jsonl"
    rc = baseline_cli.main([
        "--test-dataset", str(dataset),
        "--output", str(output),
        "--check-only",
    ])
    assert rc == 0


def test_check_only_fails_with_missing_dataset(tmp_path):
    dataset = tmp_path / "nonexistent.jsonl.gz"
    output = tmp_path / "baseline.jsonl"
    rc = baseline_cli.main([
        "--test-dataset", str(dataset),
        "--output", str(output),
        "--check-only",
    ])
    assert rc == 5
