"""Model registry (validator-05).

On a passing evaluation, copies the merged model into the Apprentice registry
at ``~/.apprentice/registry/<pattern-id>/v<n>/``, auto-increments the version
number, and writes a signed registry manifest.
"""

from __future__ import annotations

import datetime
import json
import logging
import os
import shutil
from pathlib import Path
from typing import Any

from apprentice_trainer.manifest_signer import sign_manifest, verify_manifest

LOG = logging.getLogger("apprentice_validator.registry")

DEFAULT_REGISTRY_ROOT = Path.home() / ".apprentice" / "registry"

MANIFEST_SOURCE = "training_manifest.json"
REGISTRY_MANIFEST = "registry_manifest.json"


def find_latest_version(registry_dir: Path) -> int:
    """Return the highest existing version number for a skill, or 0."""
    if not registry_dir.exists():
        return 0
    max_v = 0
    for entry in registry_dir.iterdir():
        if entry.is_dir() and entry.name.startswith("v"):
            try:
                v = int(entry.name[1:])
                if v > max_v:
                    max_v = v
            except ValueError:
                continue
    return max_v


def promote(
    *,
    pattern_id: str,
    model_dir: Path,
    scores: dict[str, Any],
    registry_root: Path | None = None,
    key_dir: Path | None = None,
) -> Path:
    """Copy merged model + training manifest into the registry.

    Directory structure:
        <registry_root>/<pattern_id>/v<N>/
            model files (config.json, model.safetensors, tokenizer, etc.)
            training_manifest.json
            training_manifest.json.sig
            registry_manifest.json
            registry_manifest.json.sig
    """
    registry_root = Path(registry_root) if registry_root else DEFAULT_REGISTRY_ROOT
    skill_dir = registry_root / pattern_id

    prev_version = find_latest_version(skill_dir)
    new_version = prev_version + 1
    dest = skill_dir / f"v{new_version}"

    if not model_dir.exists():
        raise FileNotFoundError(f"model dir not found: {model_dir}")

    training_manifest = model_dir / MANIFEST_SOURCE
    if not training_manifest.exists():
        raise FileNotFoundError(
            f"training manifest not found at {training_manifest}. "
            "Run apprentice-merge first to produce a merged model with manifest."
        )

    # Verify the training manifest signature before promoting.
    if not verify_manifest(training_manifest, key_dir):
        raise RuntimeError(
            f"training manifest signature invalid for {training_manifest}. "
            "The model was not signed by an authorized trainer — cannot promote."
        )

    dest.mkdir(parents=True, exist_ok=True)

    # Copy model files (everything in model_dir that isn't the manifest/sig).
    copied = 0
    for item in model_dir.iterdir():
        if item.name in (MANIFEST_SOURCE, f"{MANIFEST_SOURCE}.sig"):
            continue
        target = dest / item.name
        if item.is_dir():
            shutil.copytree(item, target, dirs_exist_ok=True)
        else:
            shutil.copy2(item, target)
        copied += 1

    # Copy training manifest + signature.
    shutil.copy2(training_manifest, dest / MANIFEST_SOURCE)
    sig_src = model_dir / f"{MANIFEST_SOURCE}.sig"
    if sig_src.exists():
        shutil.copy2(sig_src, dest / f"{MANIFEST_SOURCE}.sig")
    copied += 2 if sig_src.exists() else 1

    # Read base_model and merge lineage from the training manifest.
    base_model: str = "unknown"
    merged_from: list[dict[str, Any]] | None = None
    try:
        with open(training_manifest, "r", encoding="utf-8") as f:
            tm = json.load(f)
        base_model = tm.get("base_model", base_model)
        merged_from = tm.get("merged_from")
    except (OSError, json.JSONDecodeError) as e:
        LOG.warning("could not read training manifest",
                    extra={"path": str(training_manifest), "error": str(e)})

    # Write registry manifest.
    manifest: dict[str, Any] = {
        "schema_version": 1,
        "pattern_id": pattern_id,
        "version": new_version,
        "promoted_at": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
        "base_model": base_model,
        "model_dir": str(dest.resolve()),
        "scores": scores,
        "source_training_manifest": str(training_manifest.resolve()),
    }
    if merged_from:
        manifest["merged_from"] = merged_from
    reg_manifest_path = dest / REGISTRY_MANIFEST
    payload = json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    reg_manifest_path.write_text(payload, encoding="utf-8")

    # Sign the registry manifest.
    sign_manifest(reg_manifest_path, key_dir)

    LOG.info("model promoted to registry", extra={
        "pattern_id": pattern_id,
        "version": new_version,
        "dest": str(dest),
        "files_copied": copied,
        "scores": scores,
        "manifest": str(reg_manifest_path),
    })
    return dest
