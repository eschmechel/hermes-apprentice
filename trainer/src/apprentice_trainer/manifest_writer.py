"""Write a training_manifest.json next to a merged model.

The manifest captures everything a reviewer (or the validator milestone) needs
to reason about whether a candidate specialist should be promoted:

- ``dataset_hash``  — sha256 from dataset-builder's manifest.json (links the
  trained model back to the exact JSONL splits it saw).
- ``base_model``    — HF model id the LoRA adapter merged into.
- ``hyperparameters`` — all the knobs that affect convergence (lora_rank,
  batch_size, grad_accum, learning_rate, warmup_steps, max_steps, max_seq_len,
  load_in_4bit, seed). Bit-identical reruns require these to match.
- ``runtime_seconds`` — wallclock of the training step, useful for cost
  attribution and the burst dispatcher's budgeting.
- ``exit_code``      — 0 on success, non-zero on failure. We write a manifest
  even for failures so the validator has a record of what was attempted.

Signing is trainer-07's job (manifest_signer.py); this module only writes.
"""

from __future__ import annotations

import datetime
import json
import logging
import os
from pathlib import Path
from typing import Any

LOG = logging.getLogger("apprentice_trainer.manifest")

MANIFEST_FILENAME = "training_manifest.json"
SCHEMA_VERSION = 1


def read_dataset_hash(dataset_dir: Path) -> str | None:
    """Read sha256 from dataset-builder's manifest.json in dataset_dir.

    Returns None if the file is missing or doesn't carry a sha256 field — the
    training manifest still gets written, just with dataset_hash=None.
    """
    p = Path(dataset_dir) / "manifest.json"
    if not p.exists():
        return None
    try:
        with open(p, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        LOG.warning("could not read dataset manifest for hash",
                    extra={"path": str(p), "error": str(e)})
        return None
    h = data.get("sha256")
    return h if isinstance(h, str) and h else None


def build_manifest(
    *,
    dataset_dir: Path | str,
    base_model: str,
    hyperparameters: dict[str, Any],
    runtime_seconds: float,
    exit_code: int,
    extra: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Return the manifest dict (caller serializes with write_manifest)."""
    dataset_dir = Path(dataset_dir)
    return {
        "schema_version": SCHEMA_VERSION,
        "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
        "dataset_dir": str(dataset_dir),
        "dataset_hash": read_dataset_hash(dataset_dir),
        "base_model": base_model,
        "hyperparameters": dict(hyperparameters),
        "runtime_seconds": round(float(runtime_seconds), 3),
        "exit_code": int(exit_code),
        **(extra or {}),
    }


def write_manifest(output_dir: Path | str, manifest: dict[str, Any]) -> Path:
    """Atomically write manifest to <output_dir>/training_manifest.json. Returns the path."""
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    final = output_dir / MANIFEST_FILENAME
    tmp = output_dir / (MANIFEST_FILENAME + ".tmp")
    # sort_keys + 2-space indent gives a deterministic byte-stream which the
    # Ed25519 signer (trainer-07) hashes -- without sort_keys, any future Python
    # dict-order quirk would change the signature.
    payload = json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    tmp.write_text(payload, encoding="utf-8")
    os.replace(tmp, final)
    LOG.info("training manifest written",
             extra={"path": str(final), "dataset_hash": manifest.get("dataset_hash"),
                    "exit_code": manifest.get("exit_code")})
    return final


def collect_hyperparameters(args: Any) -> dict[str, Any]:
    """Extract the hyperparameter subset of a train.py argparse.Namespace.

    Kept as an explicit allow-list (not vars(args)) so we don't accidentally
    record paths, the --verbose flag, or the --check-only sentinel.
    """
    keys = (
        "base_model", "load_in_4bit", "max_seq_len", "lora_rank",
        "max_steps", "batch_size", "grad_accum",
        "learning_rate", "warmup_steps", "seed",
    )
    out = {}
    for k in keys:
        if hasattr(args, k):
            out[k] = getattr(args, k)
    return out
