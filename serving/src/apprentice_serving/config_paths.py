"""Resolve an adapter id to its on-disk LoRA path in the registry.

Layout: ``<registry_root>/<adapter_id>/latest/lora-adapter`` (W10 `latest`
pointer), falling back to the highest ``v<N>/lora-adapter`` if no `latest`
symlink exists yet. Root defaults to ``$APPRENTICE_REGISTRY_ROOT`` or
``~/.apprentice/registry``.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Callable


def registry_root(override: str | None = None) -> Path:
    if override:
        return Path(override).expanduser()
    env = os.environ.get("APPRENTICE_REGISTRY_ROOT")
    if env:
        return Path(env).expanduser()
    root = os.environ.get("APPRENTICE_ROOT", str(Path.home() / ".apprentice"))
    return Path(root).expanduser() / "registry"


def _latest_version_dir(parent: Path) -> Path | None:
    if not parent.is_dir():
        return None
    versions = [(int(p.name[1:]), p) for p in parent.iterdir()
                if p.is_dir() and p.name.startswith("v") and p.name[1:].isdigit()]
    return max(versions, key=lambda t: t[0])[1] if versions else None


def adapter_path_resolver(override_root: str | None = None) -> Callable[[str], str]:
    """Return ``resolve(adapter_id) -> str`` for the LoRA adapter dir."""
    root = registry_root(override_root)

    def resolve(adapter_id: str) -> str:
        skill = root / adapter_id
        latest = skill / "latest" / "lora-adapter"
        if latest.exists():
            return str(latest)
        vdir = _latest_version_dir(skill)
        if vdir is not None and (vdir / "lora-adapter").exists():
            return str(vdir / "lora-adapter")
        raise FileNotFoundError(
            f"no LoRA adapter for '{adapter_id}' under {skill} "
            "(expected latest/lora-adapter or v<N>/lora-adapter)"
        )

    return resolve
