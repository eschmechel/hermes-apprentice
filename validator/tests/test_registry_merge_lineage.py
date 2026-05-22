"""Tests for merge lineage tracking in the registry manifest (Phase 2c.3)."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from apprentice_validator import registry


@pytest.fixture
def key_dir(tmp_path: Path) -> Path:
    from apprentice_trainer.manifest_signer import ensure_keypair
    kd = tmp_path / "keys"
    ensure_keypair(kd)
    return kd


def _make_model_dir(tmp_path: Path, name: str, key_dir: Path, merged_from=None) -> Path:
    """Create a minimal merged-model directory with optional merged_from in manifest."""
    from apprentice_trainer.manifest_signer import sign_manifest

    model = tmp_path / name
    model.mkdir()
    (model / "config.json").write_text(json.dumps({"model_type": "qwen2"}))
    (model / "model-00001-of-00001.safetensors").write_bytes(b"\x00" * 64)
    (model / "tokenizer.json").write_text(json.dumps({"version": "1.0"}))

    manifest_data = {
        "schema_version": 1,
        "base_model": "Qwen/Qwen2.5-1.5B-Instruct",
        "exit_code": 0,
        "dataset_hash": "abc123",
    }
    if merged_from:
        manifest_data["merged_from"] = merged_from

    manifest = model / "training_manifest.json"
    manifest.write_text(json.dumps(manifest_data, indent=2, sort_keys=True) + "\n")
    sign_manifest(manifest, key_dir)
    return model


def test_registry_manifest_includes_merged_from(tmp_path: Path, key_dir: Path):
    """Registry manifest should include merged_from when the training manifest has it."""
    merged_from = [
        {"pattern_id": "parent-a", "records": 100},
        {"pattern_id": "parent-b", "records": 150},
    ]
    model_dir = _make_model_dir(tmp_path, "model-merged", key_dir, merged_from=merged_from)

    reg_root = tmp_path / "reg"
    dest = registry.promote(
        pattern_id="merged-skill",
        model_dir=model_dir,
        scores={"exact_match": 0.85, "f1": 0.90},
        registry_root=reg_root,
        key_dir=key_dir,
    )

    reg_manifest = dest / "registry_manifest.json"
    assert reg_manifest.exists()
    data = json.loads(reg_manifest.read_text())
    assert "merged_from" in data
    assert len(data["merged_from"]) == 2
    assert data["merged_from"] == merged_from


def test_registry_manifest_omits_merged_from_when_absent(tmp_path: Path, key_dir: Path):
    """Registry manifest should NOT include merged_from for non-merged models."""
    model_dir = _make_model_dir(tmp_path, "model-normal", key_dir)

    reg_root = tmp_path / "reg"
    dest = registry.promote(
        pattern_id="normal-skill",
        model_dir=model_dir,
        scores={"exact_match": 0.85},
        registry_root=reg_root,
        key_dir=key_dir,
    )

    reg_manifest = dest / "registry_manifest.json"
    data = json.loads(reg_manifest.read_text())
    assert "merged_from" not in data


def test_registry_manifest_preserves_base_model_lineage(tmp_path: Path, key_dir: Path):
    """Base model should still be recorded correctly alongside merged_from."""
    merged_from = [{"pattern_id": "p1", "records": 50}]
    model_dir = _make_model_dir(tmp_path, "model-base", key_dir, merged_from=merged_from)

    reg_root = tmp_path / "reg"
    dest = registry.promote(
        pattern_id="base-skill",
        model_dir=model_dir,
        scores={"exact_match": 0.8},
        registry_root=reg_root,
        key_dir=key_dir,
    )

    data = json.loads((dest / "registry_manifest.json").read_text())
    assert data["base_model"] == "Qwen/Qwen2.5-1.5B-Instruct"
    assert data["merged_from"] == merged_from
