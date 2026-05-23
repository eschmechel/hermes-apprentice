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
        "--base-model", "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit",
    ])
    # Acceptance: LoRA rank 16, base model Qwen2.5-1.5B-Instruct.
    assert args.lora_rank == 16
    assert "Qwen2.5-1.5B-Instruct" in args.base_model
    assert args.max_seq_len == 2048


def test_profile_overrides_defaults_but_loses_to_cli(tmp_path: Path, dataset_dir: Path):
    """Profile values should layer on top of built-in defaults, and the CLI
    should win over the profile."""
    profile = tmp_path / "p.yaml"
    profile.write_text(
        "batch_size: 7\n"
        "grad_accum: 9\n"
        "max_steps: 12\n"
        "load_in_4bit: false\n"
    )

    # 1) Profile alone: profile values applied.
    parser = t.build_parser()
    pre, _ = parser.parse_known_args(["--dataset-dir", str(dataset_dir),
                                       "--output-dir", str(tmp_path / "out"),
                                       "--profile", str(profile)])
    t._apply_profile(parser, t.load_profile(pre.profile))
    args = parser.parse_args(["--dataset-dir", str(dataset_dir),
                              "--output-dir", str(tmp_path / "out"),
                              "--profile", str(profile)])
    assert args.batch_size == 7
    assert args.grad_accum == 9
    assert args.max_steps == 12
    assert args.load_in_4bit is False

    # 2) Explicit CLI flag beats profile.
    parser2 = t.build_parser()
    t._apply_profile(parser2, t.load_profile(pre.profile))
    args2 = parser2.parse_args(["--dataset-dir", str(dataset_dir),
                                "--output-dir", str(tmp_path / "out"),
                                "--profile", str(profile),
                                "--batch-size", "1"])
    assert args2.batch_size == 1   # CLI override
    assert args2.grad_accum == 9   # profile still wins where CLI is silent


def test_profile_unknown_keys_are_reported_not_fatal(tmp_path: Path):
    profile = tmp_path / "p.yaml"
    profile.write_text("batch_size: 4\nunknown_key: 99\nanother_bogus: foo\n")
    parser = t.build_parser()
    data = t.load_profile(profile)
    ignored = t._apply_profile(parser, data)
    assert set(ignored) == {"unknown_key", "another_bogus"}
    # The recognized key still applied.
    args = parser.parse_args(["--dataset-dir", "/x", "--output-dir", "/y"])
    assert args.batch_size == 4


def test_profile_yamls_in_repo_are_valid(tmp_path: Path):
    """Every shipped profile YAML must load and validate against the parser."""
    profiles_dir = Path(__file__).resolve().parent.parent / "profiles"
    yamls = sorted(profiles_dir.glob("profile_*.yaml"))
    assert yamls, f"no profile yamls found under {profiles_dir}"
    for y in yamls:
        data = t.load_profile(y)
        assert isinstance(data, dict)
        parser = t.build_parser()
        ignored = t._apply_profile(parser, data)
        assert ignored == [], f"{y.name}: unknown keys {ignored}"
        # Spot-check required acceptance fields exist.
        assert "batch_size" in data, f"{y.name} missing batch_size"
        assert data["lora_rank"] == 16, f"{y.name}: lora_rank must be 16 per trainer-01 acceptance"


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
