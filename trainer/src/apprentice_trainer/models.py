"""Supported-models registry.

Reads ``supported_models.yaml`` next to this package and provides helpers for
resolving aliases, listing models, and getting default / quantized model IDs.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import yaml


def _yaml_path() -> Path:
    return Path(__file__).resolve().parent.parent.parent / "supported_models.yaml"


def load_supported_models(path: str | os.PathLike | None = None) -> list[dict[str, Any]]:
    """Return the ``base_models`` list from supported_models.yaml.

    Each entry is a dict with keys: ``id``, ``default`` (bool), ``min_vram_gb``,
    ``license``, optionally ``quantized_id`` and ``recommended_profile``.
    """
    yp = Path(path) if path else _yaml_path()
    if not yp.exists():
        raise FileNotFoundError(
            f"supported_models.yaml not found at {yp}. "
            "Install the trainer package or copy supported_models.yaml from "
            "the repository root."
        )
    with open(yp, "r", encoding="utf-8") as f:
        data = yaml.safe_load(f) or {}
    models: list[dict[str, Any]] = data.get("base_models", [])
    return models


def get_default_model(path: str | os.PathLike | None = None) -> str:
    """Return the ``id`` of the entry marked ``default: true``."""
    for m in load_supported_models(path):
        if m.get("default"):
            return m["id"]
    raise ValueError("supported_models.yaml has no entry with default: true")


def get_model_config(model_id: str, path: str | os.PathLike | None = None) -> dict[str, Any] | None:
    """Return the full config dict for a model ID (or None if not found)."""
    for m in load_supported_models(path):
        if m["id"] == model_id:
            return m
        if m.get("quantized_id") == model_id:
            return m
    return None


def resolve_model(model_id_or_alias: str | None = None,
                  *,
                  load_in_4bit: bool = True,
                  path: str | os.PathLike | None = None) -> str:
    """Resolve an alias or partial name to the full HF model ID.

    When *model_id_or_alias* is None, returns the default model.

    Supports:
    - Full HuggingFace IDs (``unsloth/Qwen2.5-1.5B-Instruct``)
    - Short aliases matching the repo name part (``Qwen2.5-1.5B-Instruct``)
    - Short aliases with just the size (``qwen2.5-1.5b``, case-insensitive)
    - ``4bit``-suffixed aliases for quantized variants

    When *load_in_4bit* is True and the matched model has a ``quantized_id``,
    returns the quantized variant.
    """
    models = load_supported_models(path)
    if model_id_or_alias is None:
        model_id_or_alias = get_default_model(path)

    alias_lower = model_id_or_alias.lower()

    # Try exact match first (full HF ID).
    for m in models:
        if m["id"] == model_id_or_alias:
            return _pick_variant(m, load_in_4bit)
        if m.get("quantized_id") == model_id_or_alias:
            return model_id_or_alias

    # Try matching on the repo name (part after /).
    for m in models:
        repo = m["id"].split("/")[-1].lower()
        if repo == alias_lower or repo.replace("-", "") == alias_lower.replace("-", ""):
            return _pick_variant(m, load_in_4bit)

    # Try matching on just the model family + size (e.g. "qwen2.5-1.5b").
    for m in models:
        repo = m["id"].split("/")[-1].lower()
        if repo.startswith(alias_lower):
            return _pick_variant(m, load_in_4bit)

    raise ValueError(
        f"Unknown model {model_id_or_alias!r}. "
        f"Available: {[m['id'] for m in models]}"
    )


def _pick_variant(model: dict[str, Any], load_in_4bit: bool) -> str:
    if load_in_4bit and model.get("quantized_id"):
        return model["quantized_id"]
    return model["id"]


def list_models(path: str | os.PathLike | None = None) -> list[dict[str, Any]]:
    """Return the formatted model list for CLI display."""
    return load_supported_models(path)
