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


LATEST_LINK = "latest"


def find_latest_version(registry_dir: Path) -> int:
    """Return the highest existing version number for a skill, or 0."""
    if not registry_dir.exists():
        return 0
    max_v = 0
    for entry in registry_dir.iterdir():
        if entry.is_dir() and not entry.is_symlink() and entry.name.startswith("v"):
            try:
                v = int(entry.name[1:])
                if v > max_v:
                    max_v = v
            except ValueError:
                continue
    return max_v


def list_versions(registry_dir: Path) -> list[int]:
    """All version numbers present for a skill, ascending."""
    if not registry_dir.exists():
        return []
    out = []
    for entry in registry_dir.iterdir():
        if entry.is_dir() and not entry.is_symlink() and entry.name.startswith("v"):
            try:
                out.append(int(entry.name[1:]))
            except ValueError:
                continue
    return sorted(out)


def _set_latest(skill_dir: Path, version: int) -> None:
    """Point ``<skill_dir>/latest`` at ``v<version>`` (relative symlink, atomic).

    The serving layer (config_paths.adapter_path_resolver) and the proxy
    registry endpoint both resolve ``latest`` first, so this is what makes
    instant rollback (demote) possible without recopying weights.
    """
    link = skill_dir / LATEST_LINK
    target = f"v{version}"
    tmp = skill_dir / f".{LATEST_LINK}.tmp"
    if tmp.exists() or tmp.is_symlink():
        tmp.unlink()
    tmp.symlink_to(target, target_is_directory=True)
    tmp.replace(link)  # atomic swap over any existing pointer


def current_version(skill_dir: Path) -> int | None:
    """Resolve the version ``latest`` points at, or the highest v<N> if no
    pointer exists yet. None when the skill has no versions."""
    link = skill_dir / LATEST_LINK
    if link.is_symlink():
        target = os.readlink(link)
        name = Path(target).name
        if name.startswith("v") and name[1:].isdigit():
            return int(name[1:])
    v = find_latest_version(skill_dir)
    return v or None


def demote(*, pattern_id: str, to_version: int | None = None,
           registry_root: Path | None = None) -> int:
    """Repoint ``latest`` to a previous good version (instant rollback).

    With ``to_version=None`` rolls back to the highest version *below* the
    current pointer. Returns the version now marked latest. Raises if there's
    nothing to roll back to.
    """
    registry_root = Path(registry_root) if registry_root else DEFAULT_REGISTRY_ROOT
    skill_dir = registry_root / pattern_id
    versions = list_versions(skill_dir)
    if not versions:
        raise FileNotFoundError(f"no versions for pattern '{pattern_id}' under {skill_dir}")

    if to_version is None:
        cur = current_version(skill_dir)
        below = [v for v in versions if cur is None or v < cur]
        if not below:
            raise ValueError(
                f"nothing to demote to for '{pattern_id}' (current=v{cur}, "
                f"available={['v%d' % v for v in versions]})"
            )
        to_version = max(below)
    elif to_version not in versions:
        raise ValueError(f"v{to_version} not in registry for '{pattern_id}'")

    _set_latest(skill_dir, to_version)
    LOG.info("demoted (latest repointed)", extra={
        "pattern_id": pattern_id, "now_latest": to_version,
    })
    return to_version


def garbage_collect(*, pattern_id: str, keep: int = 3,
                    registry_root: Path | None = None) -> list[int]:
    """Prune all but the newest ``keep`` versions, never removing the one
    ``latest`` points at. Returns the list of pruned version numbers."""
    registry_root = Path(registry_root) if registry_root else DEFAULT_REGISTRY_ROOT
    skill_dir = registry_root / pattern_id
    versions = list_versions(skill_dir)
    if len(versions) <= keep:
        return []
    cur = current_version(skill_dir)
    survivors = set(versions[-keep:])
    if cur is not None:
        survivors.add(cur)
    pruned = []
    for v in versions:
        if v in survivors:
            continue
        shutil.rmtree(skill_dir / f"v{v}", ignore_errors=True)
        pruned.append(v)
    if pruned:
        LOG.info("registry GC", extra={"pattern_id": pattern_id, "pruned": pruned,
                                       "kept": sorted(survivors)})
    return pruned


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

    # Atomic promote (W9): assemble in a sibling temp dir, then os.replace into
    # v<N>. A crash mid-build leaves the build dir (cleaned next call), never a
    # half-promoted v<N>, and `latest` is never advanced past an incomplete copy.
    skill_dir.mkdir(parents=True, exist_ok=True)
    build = skill_dir / f".v{new_version}.build"
    if build.exists():
        shutil.rmtree(build)
    build.mkdir(parents=True)

    # Copy model files (everything in model_dir that isn't the manifest/sig).
    copied = 0
    for item in model_dir.iterdir():
        if item.name in (MANIFEST_SOURCE, f"{MANIFEST_SOURCE}.sig"):
            continue
        target = build / item.name
        if item.is_dir():
            shutil.copytree(item, target, dirs_exist_ok=True)
        else:
            shutil.copy2(item, target)
        copied += 1

    # Copy training manifest + signature.
    shutil.copy2(training_manifest, build / MANIFEST_SOURCE)
    sig_src = model_dir / f"{MANIFEST_SOURCE}.sig"
    if sig_src.exists():
        shutil.copy2(sig_src, build / f"{MANIFEST_SOURCE}.sig")
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

    # Write registry manifest (model_dir points at the final dest, post-rename).
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
    reg_manifest_path = build / REGISTRY_MANIFEST
    payload = json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    reg_manifest_path.write_text(payload, encoding="utf-8")

    # Sign the registry manifest.
    sign_manifest(reg_manifest_path, key_dir)

    # Verify the build is complete before committing it (no half-promoted v<N>).
    if not (build / REGISTRY_MANIFEST).exists() or not (build / f"{REGISTRY_MANIFEST}.sig").exists():
        shutil.rmtree(build, ignore_errors=True)
        raise RuntimeError(f"promote build incomplete for {pattern_id} v{new_version}")

    # Atomic commit: rename the build dir into the final v<N>.
    os.replace(build, dest)

    # Advance the `latest` pointer (W10). Best-effort: a filesystem that can't
    # symlink shouldn't fail the promotion (readers fall back to highest v<N>).
    try:
        _set_latest(skill_dir, new_version)
    except OSError as e:
        LOG.warning("could not update 'latest' pointer (readers fall back to highest v<N>)",
                    extra={"pattern_id": pattern_id, "error": str(e)})

    LOG.info("model promoted to registry", extra={
        "pattern_id": pattern_id,
        "version": new_version,
        "dest": str(dest),
        "files_copied": copied,
        "scores": scores,
        "manifest": str(reg_manifest_path),
    })
    return dest
