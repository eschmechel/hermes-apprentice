"""Merge a LoRA adapter back into its base model and save the result.

The Apprentice training pipeline produces a LoRA adapter under
``<output_dir>/lora-adapter/`` (trainer-01). To deploy that adapter via vLLM or
any other inference stack that doesn't speak LoRA at runtime, we need a single
``merged_16bit`` model directory containing the base weights + adapter deltas
baked in.

This module wraps ``FastLanguageModel.save_pretrained_merged`` so the merge is
scriptable from CI and from the burst dispatcher (trainer-05).

Usage::

    apprentice-merge \\
        --base-model unsloth/Qwen2.5-1.5B-Instruct \\
        --adapter-dir ~/.apprentice/checkpoints/<pattern-id>/v1/lora-adapter \\
        --output-dir  ~/.apprentice/merged/<pattern-id>/v1

Output directory contains ``config.json``, ``model.safetensors`` (potentially
sharded across ``model-NNNNN-of-NNNNN.safetensors``), tokenizer files, and the
``generation_config.json`` Hermes / vLLM expect.

Like the trainer, the actual merge needs a GPU. ``--check-only`` validates
arguments + adapter contents on a CPU-only host.
"""

from __future__ import annotations

import argparse
import json
import logging
import shutil
import sys
import time
from pathlib import Path

from .train import _setup_logging  # reuse the JSON formatter

LOG = logging.getLogger("apprentice_trainer.exporter")

# Files Unsloth's adapter_config.json declares — used by the check-only path
# to confirm we're pointing at a real LoRA adapter dir, not a random folder.
_ADAPTER_CONFIG = "adapter_config.json"
_ADAPTER_WEIGHTS_CANDIDATES = ("adapter_model.safetensors", "adapter_model.bin")
# The trainer writes the (signed) training manifest next to the adapter; the
# merge carries it into the merged dir so the validator can verify + promote.
_TRAINING_MANIFEST = "training_manifest.json"


def validate_adapter_dir(adapter_dir: Path) -> dict:
    """Return the parsed adapter_config.json for adapter_dir. Raises
    FileNotFoundError if the directory doesn't look like a LoRA adapter."""
    cfg_path = adapter_dir / _ADAPTER_CONFIG
    if not cfg_path.exists():
        raise FileNotFoundError(
            f"{adapter_dir}: missing {_ADAPTER_CONFIG}. "
            "Does this path point at the lora-adapter/ directory produced by trainer-01?"
        )
    weights_present = any((adapter_dir / w).exists() for w in _ADAPTER_WEIGHTS_CANDIDATES)
    if not weights_present:
        raise FileNotFoundError(
            f"{adapter_dir}: missing adapter weights "
            f"({' or '.join(_ADAPTER_WEIGHTS_CANDIDATES)})."
        )
    with open(cfg_path, "r", encoding="utf-8") as f:
        return json.load(f)


def _normalize_base(name: str) -> str:
    """Normalize a base-model id for comparison: drop the quantization suffixes
    so Unsloth's fp16 and -bnb-4bit variants of the same model compare equal."""
    n = name.strip().rstrip("/")
    for suffix in ("-unsloth-bnb-4bit", "-bnb-4bit"):
        if n.endswith(suffix):
            n = n[: -len(suffix)]
    return n.lower()


def merge_and_export(
    *,
    base_model: str,
    adapter_dir: Path,
    output_dir: Path,
    max_seq_len: int = 2048,
    save_method: str = "merged_16bit",
) -> Path:
    """Load base + adapter, merge, save to output_dir. Returns output_dir.

    Local imports keep --help and the check-only path usable on CPU-only hosts
    without torch/unsloth installed.
    """
    try:
        import torch
    except ImportError as e:
        raise RuntimeError(
            "torch is not installed; install the trainer with the GPU extras."
        ) from e
    if not torch.cuda.is_available():
        raise RuntimeError(
            "CUDA is not available — merging needs a GPU host. "
            "Run this on the 4060 laptop or RunPod A100."
        )
    from unsloth import FastLanguageModel

    output_dir.mkdir(parents=True, exist_ok=True)

    LOG.info(
        "loading base + adapter for merge",
        extra={"base_model": base_model, "adapter_dir": str(adapter_dir),
               "max_seq_len": max_seq_len},
    )
    # A LoRA adapter can only be merged onto the exact base it was trained
    # against, which is recorded in adapter_config.json's
    # base_model_name_or_path. So we load from the adapter dir — Unsloth
    # resolves base + adapter (and maps -bnb-4bit -> fp16) in one shot. The
    # --base-model argument is a sanity check, not an override: warn if it
    # disagrees with the recorded base rather than silently merging onto the
    # wrong weights.
    recorded_base = validate_adapter_dir(adapter_dir).get("base_model_name_or_path")
    if base_model and recorded_base and _normalize_base(base_model) != _normalize_base(recorded_base):
        LOG.warning(
            "requested --base-model disagrees with the adapter's recorded base; "
            "using the recorded base (the adapter cannot merge onto a different base)",
            extra={"requested_base_model": base_model, "adapter_base": recorded_base},
        )

    model, tokenizer = FastLanguageModel.from_pretrained(
        model_name=str(adapter_dir),
        max_seq_length=max_seq_len,
        dtype=None,
        load_in_4bit=False,  # required: merge needs the full-precision base
    )

    t0 = time.time()
    LOG.info("saving merged_16bit", extra={"output_dir": str(output_dir),
                                            "save_method": save_method})
    model.save_pretrained_merged(str(output_dir), tokenizer, save_method=save_method)
    LOG.info("merge complete", extra={"wallclock_seconds": round(time.time() - t0, 1),
                                       "output_dir": str(output_dir)})

    # Carry the (signed) training manifest into the merged dir so the validator
    # can verify provenance and promote. The trainer writes it next to the
    # adapter, i.e. in adapter_dir's parent.
    manifest_src_dir = adapter_dir.parent
    for name in (_TRAINING_MANIFEST, f"{_TRAINING_MANIFEST}.sig"):
        src = manifest_src_dir / name
        if src.exists():
            shutil.copy2(src, output_dir / name)
            LOG.info("copied training manifest into merged dir", extra={"file": name})
        elif name == _TRAINING_MANIFEST:
            LOG.warning(
                "training manifest not found next to adapter; the validator will "
                "refuse to promote without it (run apprentice-sign sign on it first)",
                extra={"expected": str(src)},
            )

    # Carry the LoRA adapter itself into the merged dir so promote lands it in
    # the registry — multi-LoRA serving loads the ~18 MB adapter (the merged
    # fp16 model is export-only). Skip if adapter_dir already IS output_dir's child.
    adapter_copy = output_dir / "lora-adapter"
    if adapter_dir.resolve() != adapter_copy.resolve():
        shutil.copytree(adapter_dir, adapter_copy, dirs_exist_ok=True)
        LOG.info("copied lora-adapter into merged dir for registry/serving",
                 extra={"adapter_dir": str(adapter_copy)})
    return output_dir


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-merge",
        description="Merge a LoRA adapter back into its base model and save as merged_16bit.",
    )
    p.add_argument("--base-model", required=True,
                   help="HF model id of the base the adapter was trained on. The "
                        "actual base is read from the adapter's adapter_config.json; "
                        "this value is validated against it (a mismatch only warns).")
    p.add_argument("--adapter-dir", required=True,
                   help="Path to the LoRA adapter dir produced by apprentice-train "
                        "(usually <output>/lora-adapter).")
    p.add_argument("--output-dir", required=True,
                   help="Where to write the merged model.")
    p.add_argument("--max-seq-len", type=int, default=2048)
    p.add_argument("--save-method", default="merged_16bit",
                   choices=["merged_16bit", "merged_4bit", "lora"],
                   help="Unsloth save method. merged_16bit produces a deployable "
                        "fp16 model; merged_4bit keeps quantization; lora saves only "
                        "the adapter (already done by the trainer).")
    p.add_argument("--check-only", action="store_true",
                   help="Validate args + adapter dir without invoking unsloth.")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def check_only(args: argparse.Namespace) -> int:
    adapter_dir = Path(args.adapter_dir).expanduser().resolve()
    cfg = validate_adapter_dir(adapter_dir)
    LOG.info("check-only passed", extra={
        "adapter_dir": str(adapter_dir),
        "adapter_target_modules": cfg.get("target_modules"),
        "adapter_r": cfg.get("r"),
        "adapter_base_model_name_or_path": cfg.get("base_model_name_or_path"),
        "would_save_method": args.save_method,
        "would_output_dir": str(Path(args.output_dir).expanduser().resolve()),
    })
    return 0


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    _setup_logging(logging.DEBUG if args.verbose else logging.INFO)

    try:
        if args.check_only:
            return check_only(args)
        merge_and_export(
            base_model=args.base_model,
            adapter_dir=Path(args.adapter_dir).expanduser().resolve(),
            output_dir=Path(args.output_dir).expanduser().resolve(),
            max_seq_len=args.max_seq_len,
            save_method=args.save_method,
        )
    except (FileNotFoundError, RuntimeError) as e:
        LOG.error("merge failed", extra={"error": str(e)})
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
