"""Tests for validator-05: model registry. CPU-only."""

from __future__ import annotations

import json
import shutil
from pathlib import Path

import pytest

from apprentice_validator import registry


@pytest.fixture
def key_dir(tmp_path: Path) -> Path:
    """Create an Ed25519 keypair in a temp dir for signature tests."""
    from apprentice_trainer.manifest_signer import ensure_keypair
    kd = tmp_path / "keys"
    ensure_keypair(kd)
    return kd


@pytest.fixture
def fake_model_dir(tmp_path: Path, key_dir: Path) -> Path:
    """Create a minimal merged-model directory structure with a signed manifest."""
    from apprentice_trainer.manifest_signer import sign_manifest

    model = tmp_path / "model"
    model.mkdir()
    (model / "config.json").write_text(json.dumps({"model_type": "qwen2"}))
    (model / "model-00001-of-00001.safetensors").write_bytes(b"\x00" * 64)
    (model / "tokenizer.json").write_text(json.dumps({"version": "1.0"}))

    manifest = model / "training_manifest.json"
    manifest.write_text(
        json.dumps({"schema_version": 1, "base_model": "Qwen/Qwen2.5-1.5B-Instruct",
                     "exit_code": 0, "dataset_hash": "abc123"}, indent=2, sort_keys=True) + "\n"
    )
    sign_manifest(manifest, key_dir)
    return model


def test_find_latest_version_empty(tmp_path: Path):
    skill_dir = tmp_path / "test-skill"
    assert registry.find_latest_version(skill_dir) == 0


def test_find_latest_version_existing(tmp_path: Path):
    skill_dir = tmp_path / "test-skill"
    skill_dir.mkdir(parents=True)
    (skill_dir / "v1").mkdir()
    (skill_dir / "v3").mkdir()
    (skill_dir / "v2").mkdir()
    (skill_dir / "not-a-version").mkdir()
    assert registry.find_latest_version(skill_dir) == 3


def test_promote_copies_model_files(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    dest = registry.promote(
        pattern_id="demo-skill",
        model_dir=fake_model_dir,
        scores={"exact_match": 0.85, "f1": 0.90},
        registry_root=reg_root,
        key_dir=key_dir,
    )
    assert dest.exists()
    assert dest.parent.name == "demo-skill"
    assert dest.name == "v1"
    assert (dest / "config.json").exists()
    assert (dest / "model-00001-of-00001.safetensors").exists()
    assert (dest / "tokenizer.json").exists()


def test_promote_writes_registry_manifest(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    dest = registry.promote(
        pattern_id="demo-skill",
        model_dir=fake_model_dir,
        scores={"exact_match": 0.85, "f1": 0.90},
        registry_root=reg_root,
        key_dir=key_dir,
    )
    reg_manifest = dest / "registry_manifest.json"
    assert reg_manifest.exists()
    data = json.loads(reg_manifest.read_text())
    assert data["pattern_id"] == "demo-skill"
    assert data["version"] == 1
    assert data["scores"]["exact_match"] == 0.85
    assert data["scores"]["f1"] == 0.90


def test_promote_signs_registry_manifest(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    dest = registry.promote(
        pattern_id="demo-skill",
        model_dir=fake_model_dir,
        scores={"exact_match": 0.85},
        registry_root=reg_root,
        key_dir=key_dir,
    )
    sig = dest / "registry_manifest.json.sig"
    assert sig.exists()
    assert sig.read_bytes()


def test_promote_increments_version(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    registry.promote(
        pattern_id="inc-skill",
        model_dir=fake_model_dir,
        scores={"exact_match": 0.7},
        registry_root=reg_root,
        key_dir=key_dir,
    )
    dest2 = registry.promote(
        pattern_id="inc-skill",
        model_dir=fake_model_dir,
        scores={"exact_match": 0.9},
        registry_root=reg_root,
        key_dir=key_dir,
    )
    assert dest2.name == "v2"
    assert (reg_root / "inc-skill" / "v1").exists()
    assert (reg_root / "inc-skill" / "v2").exists()


def test_promote_raises_missing_model():
    with pytest.raises(FileNotFoundError, match="model dir not found"):
        registry.promote(
            pattern_id="x",
            model_dir=Path("/nonexistent/path"),
            scores={},
        )
