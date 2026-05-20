"""Unsloth QLoRA fine-tuning entrypoint for the Apprentice pipeline.

Reads a versioned dataset directory produced by ``dataset-builder``
(``~/.apprentice/datasets/<pattern>/v<N>/{train,val,test}.jsonl.gz``), loads
Qwen2.5-1.5B-Instruct in 4-bit, attaches a LoRA rank-16 adapter, runs SFT,
and saves the LoRA adapter to ``--output-dir``.

Merged-model export, training manifest, and signing live in sibling modules
(trainer-04 / 06 / 07). This script is the LoRA-adapter producer.

Usage (after `uv pip install -e .[gpu]` — see README for the CUDA-specific extra):

    apprentice-train \\
        --dataset-dir ~/.apprentice/datasets/<pattern-id>/v1 \\
        --output-dir   ~/.apprentice/checkpoints/<pattern-id>/v1 \\
        --max-steps    60 \\
        --batch-size   2 \\
        --grad-accum   4
"""

from __future__ import annotations

import argparse
import gzip
import json
import logging
import os
import sys
import time
from pathlib import Path
from typing import Any

import yaml

LOG = logging.getLogger("apprentice_trainer")

# logging.LogRecord built-in attrs we never want in the JSON payload (Python
# adds new ones each release — `taskName` arrived in 3.12). Anything NOT in
# this set is treated as caller-supplied `extra=...`.
_LOG_RECORD_STD_ATTRS = frozenset({
    "args", "msg", "name", "levelname", "levelno", "pathname", "filename",
    "module", "exc_info", "exc_text", "stack_info", "lineno", "funcName",
    "created", "msecs", "relativeCreated", "thread", "threadName",
    "processName", "process", "taskName",
})


# ---------------------------------------------------------------------------
# Logging — JSON to stderr, matching observer/detector/dataset-builder style.
# ---------------------------------------------------------------------------

class _JSONFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(record.created)),
            "level": record.levelname,
            "msg": record.getMessage(),
            "component": record.name,
        }
        # Surface any extra=... fields the caller passed in.
        for k, v in record.__dict__.items():
            if k in _LOG_RECORD_STD_ATTRS:
                continue
            payload[k] = v
        return json.dumps(payload, default=str, ensure_ascii=False)


def _setup_logging(level: int = logging.INFO) -> None:
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(_JSONFormatter())
    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(level)


# ---------------------------------------------------------------------------
# Dataset loading
# ---------------------------------------------------------------------------

def _iter_jsonl_gz(path: Path):
    with gzip.open(path, "rt", encoding="utf-8") as fp:
        for line_num, line in enumerate(fp, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                yield json.loads(line)
            except json.JSONDecodeError as e:
                raise ValueError(f"{path}:{line_num}: {e}") from e


def load_dataset_jsonl_gz(dataset_dir: Path):
    """Return (train_records, val_records) lists of dicts in HF chat format.

    val.jsonl.gz is optional — returns an empty list if absent so the caller
    can decide whether to skip evaluation.
    """
    train_path = dataset_dir / "train.jsonl.gz"
    val_path = dataset_dir / "val.jsonl.gz"
    if not train_path.exists():
        raise FileNotFoundError(f"missing {train_path}")

    train_records = list(_iter_jsonl_gz(train_path))
    val_records = list(_iter_jsonl_gz(val_path)) if val_path.exists() else []
    return train_records, val_records


# ---------------------------------------------------------------------------
# Training
# ---------------------------------------------------------------------------

def run_training(args: argparse.Namespace) -> int:
    # Imports are local so `apprentice-train --help` works on a pure-CPU host
    # without unsloth/torch installed.
    try:
        import torch
    except ImportError:
        LOG.error("torch is not installed. See trainer/README.md for the CUDA install command.")
        return 2

    if not torch.cuda.is_available():
        LOG.error(
            "CUDA is not available — Unsloth requires an NVIDIA GPU. "
            "Run this on a machine with a GPU (e.g. the laptop's 4060 or a RunPod A100). "
            "See trainer-05 for the cloud-burst dispatcher.",
        )
        return 3

    try:
        # Unsloth MUST be imported before trl/transformers/peft so its
        # optimizations are applied — otherwise LoRA params are not marked
        # trainable ("Trainable parameters = 0") and training raises at step 0.
        from unsloth import FastLanguageModel
        from unsloth.chat_templates import get_chat_template
        from trl import SFTTrainer, SFTConfig
        from datasets import Dataset
    except ImportError as e:
        LOG.error("missing training dep: %s. See trainer/README.md for the install command.", e)
        return 2

    dataset_dir = Path(args.dataset_dir).expanduser().resolve()
    output_dir = Path(args.output_dir).expanduser().resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    adapter_dir = output_dir / "lora-adapter"

    LOG.info("trainer starting",
             extra={"dataset_dir": str(dataset_dir),
                    "output_dir": str(output_dir),
                    "base_model": args.base_model,
                    "lora_rank": args.lora_rank,
                    "max_seq_len": args.max_seq_len,
                    "max_steps": args.max_steps,
                    "batch_size": args.batch_size,
                    "grad_accum": args.grad_accum,
                    "learning_rate": args.learning_rate,
                    "cuda_device": torch.cuda.get_device_name(0),
                    "cuda_capability": ".".join(map(str, torch.cuda.get_device_capability(0)))})

    train_records, val_records = load_dataset_jsonl_gz(dataset_dir)
    LOG.info("dataset loaded",
             extra={"train_count": len(train_records), "val_count": len(val_records)})
    if not train_records:
        LOG.error("train.jsonl.gz is empty")
        return 4

    # ---- model -----------------------------------------------------------
    LOG.info(
        "loading base model",
        extra={"base_model": args.base_model, "load_in_4bit": args.load_in_4bit},
    )
    model, tokenizer = FastLanguageModel.from_pretrained(
        model_name=args.base_model,
        max_seq_length=args.max_seq_len,
        dtype=None,  # let Unsloth pick (bf16 on Ampere+, fp16 otherwise)
        load_in_4bit=args.load_in_4bit,
    )

    # Qwen2.5 uses ChatML; Unsloth's preset names this template "qwen-2.5".
    tokenizer = get_chat_template(tokenizer, chat_template="qwen-2.5")

    # ---- LoRA -----------------------------------------------------------
    model = FastLanguageModel.get_peft_model(
        model,
        r=args.lora_rank,
        target_modules=[
            "q_proj", "k_proj", "v_proj", "o_proj",
            "gate_proj", "up_proj", "down_proj",
        ],
        lora_alpha=args.lora_rank,  # alpha == rank is a common, well-behaved default
        lora_dropout=0.0,
        bias="none",
        use_gradient_checkpointing="unsloth",
        random_state=args.seed,
        use_rslora=False,
        loftq_config=None,
    )

    # ---- format dataset --------------------------------------------------
    def _format(example):
        # `messages` arrives as a list of {role, content} dicts produced by
        # dataset-builder's splitter (Hermes chat template).
        text = tokenizer.apply_chat_template(
            example["messages"],
            tokenize=False,
            add_generation_prompt=False,
        )
        return {"text": text}

    train_ds = Dataset.from_list(train_records).map(_format, batched=False)
    LOG.info("dataset formatted", extra={"rows": len(train_ds)})

    # ---- trainer ---------------------------------------------------------
    bf16_supported = torch.cuda.is_bf16_supported()
    # TRL 0.24 moved the SFT-specific knobs (dataset_text_field, max_length,
    # dataset_num_proc, packing) onto SFTConfig, and renamed SFTTrainer's
    # `tokenizer` kwarg to `processing_class`.
    training_args = SFTConfig(
        per_device_train_batch_size=args.batch_size,
        gradient_accumulation_steps=args.grad_accum,
        warmup_steps=args.warmup_steps,
        max_steps=args.max_steps,
        learning_rate=args.learning_rate,
        fp16=not bf16_supported,
        bf16=bf16_supported,
        logging_steps=1,
        optim="adamw_8bit",
        weight_decay=0.01,
        lr_scheduler_type="linear",
        seed=args.seed,
        output_dir=str(output_dir / "checkpoints"),
        report_to="none",
        save_strategy="no",  # we save the final LoRA explicitly below
        dataset_text_field="text",
        max_length=args.max_seq_len,
        dataset_num_proc=2,
        packing=False,
    )

    # unsloth_zoo's generated SFTTrainer injects the unresolved placeholders
    # "<EOS_TOKEN>" / "<PAD_TOKEN>" as the trainer's eos/pad tokens; TRL then
    # rejects them because convert_tokens_to_ids() can't find those literals in
    # the vocab. Map the placeholders to the real special tokens so validation
    # passes. This only affects the id lookup — TRL uses the real
    # tokenizer.eos_token string for actual sequence handling.
    _placeholder_tokens = {
        "<EOS_TOKEN>": tokenizer.eos_token,
        "<PAD_TOKEN>": tokenizer.pad_token or tokenizer.eos_token,
    }
    _orig_convert = tokenizer.convert_tokens_to_ids

    def _convert_tokens_to_ids(tokens, *a, **kw):
        if isinstance(tokens, str) and tokens in _placeholder_tokens:
            tokens = _placeholder_tokens[tokens]
        return _orig_convert(tokens, *a, **kw)

    tokenizer.convert_tokens_to_ids = _convert_tokens_to_ids

    trainer = SFTTrainer(
        model=model,
        processing_class=tokenizer,
        train_dataset=train_ds,
        args=training_args,
    )

    # ---- train + save ----------------------------------------------------
    # The training+manifest block is wrapped so a crash mid-train still leaves
    # a manifest describing what was attempted. The validator milestone treats
    # exit_code != 0 as "do not promote", so the manifest must be written even
    # on failure.
    from . import manifest_writer

    t0 = time.time()
    LOG.info("training begins")
    exit_code = 0
    try:
        stats = trainer.train()
        train_time = time.time() - t0
        LOG.info(
            "training complete",
            extra={
                "wallclock_seconds": round(train_time, 1),
                "train_runtime_seconds": getattr(stats.metrics, "get", lambda *_: None)("train_runtime"),
                "train_loss": getattr(stats.metrics, "get", lambda *_: None)("train_loss"),
                "steps": stats.global_step,
            },
        )
        adapter_dir.mkdir(parents=True, exist_ok=True)
        model.save_pretrained(str(adapter_dir))
        tokenizer.save_pretrained(str(adapter_dir))
        LOG.info("lora adapter saved", extra={"path": str(adapter_dir)})
    except Exception:
        train_time = time.time() - t0
        exit_code = 1
        LOG.exception("training raised")

    manifest = manifest_writer.build_manifest(
        dataset_dir=dataset_dir,
        base_model=args.base_model,
        hyperparameters=manifest_writer.collect_hyperparameters(args),
        runtime_seconds=train_time,
        exit_code=exit_code,
        extra={"adapter_dir": str(adapter_dir) if exit_code == 0 else None},
    )
    manifest_writer.write_manifest(output_dir, manifest)
    return exit_code


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-train",
        description="Unsloth QLoRA fine-tuning on a dataset-builder output directory.",
    )
    p.add_argument("--dataset-dir", required=True,
                   help="Path to a versioned dataset dir (contains train.jsonl.gz, val.jsonl.gz).")
    p.add_argument("--output-dir", required=True,
                   help="Where to write the LoRA adapter + training checkpoints.")
    p.add_argument("--profile", default=os.environ.get("APPRENTICE_TRAINER_PROFILE"),
                   help="YAML profile whose keys override built-in defaults. "
                        "Profile keys correspond to long-option dest names "
                        "(e.g. base_model, batch_size). Explicit CLI flags still win. "
                        "Defaults to $APPRENTICE_TRAINER_PROFILE so users can drop a "
                        "personal profile at e.g. ~/.config/apprentice-trainer/local.yaml "
                        "and have it auto-applied.")
    p.add_argument("--base-model", default="unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit",
                   help="HF model id; defaults to the Unsloth-prequantized Qwen2.5-1.5B.")
    p.add_argument("--load-in-4bit", action=argparse.BooleanOptionalAction, default=True,
                   help="Whether to bnb-4bit-quantize the base model (default True). "
                        "Set --no-load-in-4bit on big-VRAM machines (A100, H100) where "
                        "16-bit LoRA on the unquantized base trains faster.")
    p.add_argument("--max-seq-len", type=int, default=2048)
    p.add_argument("--lora-rank", type=int, default=16, help="LoRA rank r (acceptance: 16).")
    p.add_argument("--max-steps", type=int, default=60)
    p.add_argument("--batch-size", type=int, default=2)
    p.add_argument("--grad-accum", type=int, default=4)
    p.add_argument("--learning-rate", type=float, default=2e-4)
    p.add_argument("--warmup-steps", type=int, default=5)
    p.add_argument("--seed", type=int, default=3407)
    p.add_argument("--check-only", action="store_true",
                   help="Parse args + load dataset + validate config, then exit without training.")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def load_profile(path: str | os.PathLike) -> dict[str, Any]:
    """Load a YAML profile file. Returns a dict of argparse-dest -> value.

    Unknown keys are kept in the dict (caller can validate against the parser's
    actions). Missing file raises FileNotFoundError; bad YAML raises a
    yaml.YAMLError with the path-prefixed message.
    """
    p = Path(path).expanduser().resolve()
    with open(p, "r", encoding="utf-8") as f:
        try:
            data = yaml.safe_load(f) or {}
        except yaml.YAMLError as e:
            raise yaml.YAMLError(f"{p}: {e}") from e
    if not isinstance(data, dict):
        raise ValueError(f"{p}: top-level YAML must be a mapping, got {type(data).__name__}")
    return data


def _apply_profile(parser: argparse.ArgumentParser, profile: dict[str, Any]) -> list[str]:
    """Set profile values as parser defaults so CLI flags still override.

    Returns a list of profile keys that didn't match any parser dest (warnings,
    not fatal — profiles can carry extra metadata like `comment`).
    """
    known_dests = {action.dest for action in parser._actions}
    accepted = {k: v for k, v in profile.items() if k in known_dests}
    parser.set_defaults(**accepted)
    return [k for k in profile if k not in known_dests]


def check_only(args: argparse.Namespace) -> int:
    """Lightweight verification path that doesn't need CUDA/Unsloth: parses args,
    loads the dataset, sanity-checks each row has a `messages` field, and exits.
    Lets `--help` and structural validation work on a pure-CPU host."""
    dataset_dir = Path(args.dataset_dir).expanduser().resolve()
    train_records, val_records = load_dataset_jsonl_gz(dataset_dir)
    bad = []
    for i, r in enumerate(train_records[:50]):  # sample first 50
        if not isinstance(r, dict) or "messages" not in r:
            bad.append(i)
        elif not isinstance(r["messages"], list) or not r["messages"]:
            bad.append(i)
    if bad:
        LOG.error("dataset rows malformed", extra={"bad_indices": bad[:10]})
        return 5
    LOG.info("check-only passed",
             extra={"dataset_dir": str(dataset_dir),
                    "train_count": len(train_records),
                    "val_count": len(val_records),
                    "first_row_role_chain": [m["role"] for m in train_records[0]["messages"]]
                    if train_records else []})
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    # Two-pass: first peek at --profile so we can layer its values BENEATH any
    # explicit CLI overrides via set_defaults(). argparse then resolves CLI
    # flags on top normally, giving precedence: CLI > profile > built-in default.
    prelim, _unknown = parser.parse_known_args(argv)
    ignored: list[str] = []
    if prelim.profile:
        profile = load_profile(prelim.profile)
        ignored = _apply_profile(parser, profile)
    args = parser.parse_args(argv)
    _setup_logging(logging.DEBUG if args.verbose else logging.INFO)
    if ignored:
        LOG.warning("profile had unknown keys (ignored)",
                    extra={"profile": prelim.profile, "unknown": ignored})

    if args.check_only:
        return check_only(args)
    return run_training(args)


if __name__ == "__main__":
    raise SystemExit(main())
