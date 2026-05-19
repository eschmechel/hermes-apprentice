"""CPU-friendly tests for the merge/export entrypoint.

The actual save_pretrained_merged call requires CUDA; we don't test it here.
We DO test the adapter-dir validator and the --check-only path so a botched
adapter directory surfaces a clear error before paying for an A100 minute.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from apprentice_trainer import model_exporter as me


def _make_adapter(adapter_dir: Path, *, weights_filename: str = "adapter_model.safetensors") -> dict:
    adapter_dir.mkdir(parents=True, exist_ok=True)
    cfg = {
        "base_model_name_or_path": "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit",
        "r": 16,
        "lora_alpha": 16,
        "target_modules": ["q_proj", "k_proj", "v_proj", "o_proj"],
        "task_type": "CAUSAL_LM",
    }
    (adapter_dir / "adapter_config.json").write_text(json.dumps(cfg))
    (adapter_dir / weights_filename).write_bytes(b"\x00" * 16)  # placeholder
    return cfg


def test_validate_adapter_dir_happy(tmp_path: Path):
    adapter = tmp_path / "lora-adapter"
    _make_adapter(adapter)
    cfg = me.validate_adapter_dir(adapter)
    assert cfg["r"] == 16
    assert "q_proj" in cfg["target_modules"]


def test_validate_adapter_dir_missing_config(tmp_path: Path):
    with pytest.raises(FileNotFoundError) as exc:
        me.validate_adapter_dir(tmp_path)
    assert "adapter_config.json" in str(exc.value)


def test_validate_adapter_dir_missing_weights(tmp_path: Path):
    adapter = tmp_path / "lora-adapter"
    adapter.mkdir()
    (adapter / "adapter_config.json").write_text(json.dumps({"r": 16}))
    with pytest.raises(FileNotFoundError) as exc:
        me.validate_adapter_dir(adapter)
    assert "adapter_model" in str(exc.value)


def test_validate_adapter_dir_accepts_bin_weights(tmp_path: Path):
    # Pre-safetensors training runs save adapter_model.bin instead. Both legal.
    adapter = tmp_path / "lora-adapter"
    _make_adapter(adapter, weights_filename="adapter_model.bin")
    cfg = me.validate_adapter_dir(adapter)
    assert cfg["r"] == 16


def test_check_only_exit_code_zero(tmp_path: Path):
    adapter = tmp_path / "lora-adapter"
    _make_adapter(adapter)
    rc = me.main([
        "--base-model", "unsloth/Qwen2.5-1.5B-Instruct",
        "--adapter-dir", str(adapter),
        "--output-dir", str(tmp_path / "merged"),
        "--check-only",
    ])
    assert rc == 0


def test_check_only_exits_nonzero_on_bad_adapter(tmp_path: Path):
    # No adapter dir at all -> validate_adapter_dir raises -> main returns 1.
    rc = me.main([
        "--base-model", "unsloth/Qwen2.5-1.5B-Instruct",
        "--adapter-dir", str(tmp_path / "does-not-exist"),
        "--output-dir", str(tmp_path / "merged"),
        "--check-only",
    ])
    # check_only re-raises through main's try/except; FileNotFoundError → exit 1.
    # If the test framework catches the raw exception, treat that as failure too.
    assert rc != 0


def test_parser_save_method_default_is_merged_16bit():
    args = me.build_parser().parse_args([
        "--base-model", "x", "--adapter-dir", "/x", "--output-dir", "/y",
    ])
    assert args.save_method == "merged_16bit"
