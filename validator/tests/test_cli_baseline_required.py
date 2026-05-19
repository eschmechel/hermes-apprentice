"""CLI smoke tests for apprentice-validate's baseline-pairs requirement.

These don't touch a GPU — they just check argparse + the early validation
that --baseline-pairs is required outside --check-only.
"""

from __future__ import annotations

import gzip
import json
from pathlib import Path

from apprentice_validator import cli


def _write_test_dataset(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with gzip.open(path, "wt", encoding="utf-8") as fp:
        fp.write(json.dumps({"messages": [
            {"role": "user", "content": "hi"},
            {"role": "assistant", "content": "hello"},
        ]}) + "\n")


def test_validate_requires_baseline_pairs_outside_check_only(tmp_path: Path, capsys):
    model_dir = tmp_path / "model"
    model_dir.mkdir()
    (model_dir / "config.json").write_text("{}")

    test_ds = tmp_path / "test.jsonl.gz"
    _write_test_dataset(test_ds)

    # No --baseline-pairs → must fail with our specific exit code without
    # ever importing vLLM.
    rc = cli.main([
        "--model-dir", str(model_dir),
        "--test-dataset", str(test_ds),
        "--pattern-id", "demo",
    ])
    assert rc == 6, f"expected exit code 6 (missing --baseline-pairs), got {rc}"


def test_check_only_does_not_require_baseline_pairs(tmp_path: Path):
    model_dir = tmp_path / "model"
    model_dir.mkdir()
    (model_dir / "config.json").write_text("{}")

    test_ds = tmp_path / "test.jsonl.gz"
    _write_test_dataset(test_ds)

    rc = cli.main([
        "--model-dir", str(model_dir),
        "--test-dataset", str(test_ds),
        "--pattern-id", "demo",
        "--check-only",
    ])
    assert rc == 0
