"""Tests for validator-05: model registry. CPU-only."""

from __future__ import annotations

import json
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


# ── W10: latest pointer + demote + gc ────────────────────────────────────────

def test_promote_sets_latest_pointer(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    for _ in range(2):
        registry.promote(pattern_id="ptr", model_dir=fake_model_dir,
                         scores={"f1": 0.9}, registry_root=reg_root, key_dir=key_dir)
    skill_dir = reg_root / "ptr"
    latest = skill_dir / "latest"
    assert latest.is_symlink()
    assert (latest / "registry_manifest.json").exists()  # resolves through the link
    assert registry.current_version(skill_dir) == 2


def test_find_latest_version_ignores_latest_symlink(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    registry.promote(pattern_id="s", model_dir=fake_model_dir, scores={},
                     registry_root=reg_root, key_dir=key_dir)
    # 'latest' is a symlink, not a v<N> dir — must not be miscounted.
    assert registry.find_latest_version(reg_root / "s") == 1
    assert registry.list_versions(reg_root / "s") == [1]


def test_demote_repoints_latest(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    for _ in range(3):
        registry.promote(pattern_id="d", model_dir=fake_model_dir, scores={},
                         registry_root=reg_root, key_dir=key_dir)
    skill_dir = reg_root / "d"
    assert registry.current_version(skill_dir) == 3
    now = registry.demote(pattern_id="d", registry_root=reg_root)
    assert now == 2 and registry.current_version(skill_dir) == 2
    now = registry.demote(pattern_id="d", to_version=1, registry_root=reg_root)
    assert now == 1 and registry.current_version(skill_dir) == 1


def test_demote_nothing_below_raises(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    registry.promote(pattern_id="only", model_dir=fake_model_dir, scores={},
                     registry_root=reg_root, key_dir=key_dir)
    with pytest.raises(ValueError):
        registry.demote(pattern_id="only", registry_root=reg_root)


def test_gc_keeps_newest_and_latest(tmp_path: Path, fake_model_dir: Path, key_dir: Path):
    reg_root = tmp_path / "reg"
    for _ in range(5):
        registry.promote(pattern_id="g", model_dir=fake_model_dir, scores={},
                         registry_root=reg_root, key_dir=key_dir)
    # roll latest back to v2, then GC keeping 2 newest -> keep {v4,v5} + latest v2
    registry.demote(pattern_id="g", to_version=2, registry_root=reg_root)
    pruned = registry.garbage_collect(pattern_id="g", keep=2, registry_root=reg_root)
    assert set(pruned) == {1, 3}
    assert registry.list_versions(reg_root / "g") == [2, 4, 5]
    assert registry.current_version(reg_root / "g") == 2  # latest untouched


# ── W9: atomic promote ───────────────────────────────────────────────────────

def test_promote_is_atomic_no_partial_version(tmp_path, fake_model_dir, key_dir, monkeypatch):
    """If signing fails mid-promote, no v<N> directory is left behind (the build
    happens in a temp dir that's only renamed into place once complete)."""
    from apprentice_validator import registry as reg
    reg_root = tmp_path / "reg"

    def boom(*a, **k):
        raise RuntimeError("signing blew up")

    monkeypatch.setattr(reg, "sign_manifest", boom)
    with pytest.raises(RuntimeError):
        reg.promote(pattern_id="atomic", model_dir=fake_model_dir, scores={},
                    registry_root=reg_root, key_dir=key_dir)

    skill = reg_root / "atomic"
    assert not (skill / "v1").exists()          # no half-promoted version
    assert reg.current_version(skill) is None    # latest never advanced
