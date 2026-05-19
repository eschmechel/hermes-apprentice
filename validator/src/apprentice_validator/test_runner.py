"""Specialist test runner (validator-01).

Loads test.jsonl.gz, formats as Hermes chat template, runs through the merged
specialist model via vLLM offline batch API, and returns (expected, actual) pairs.

vLLM imports are deferred so ``--help`` and ``--check-only`` work on CPU hosts.
"""

from __future__ import annotations

import gzip
import json
import logging
from pathlib import Path
from typing import Any

LOG = logging.getLogger("apprentice_validator.runner")

Pair = dict[str, Any]


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


def load_test_dataset(path: Path) -> list[dict[str, Any]]:
    """Load test.jsonl.gz records. Each record is {messages: [{role, content}]}."""
    if not path.exists():
        raise FileNotFoundError(f"test dataset not found: {path}")
    records = list(_iter_jsonl_gz(path))
    if not records:
        raise ValueError(f"test dataset is empty: {path}")
    for i, r in enumerate(records):
        if not isinstance(r, dict) or "messages" not in r:
            raise ValueError(f"{path}: record {i} missing 'messages' field")
        msgs = r["messages"]
        if not isinstance(msgs, list) or not msgs:
            raise ValueError(f"{path}: record {i} has empty/invalid messages")
    return records


def _extract_expected(record: dict[str, Any]) -> str | None:
    """Extract the last assistant message content from a record."""
    for msg in reversed(record["messages"]):
        if msg.get("role") == "assistant":
            return msg.get("content", "")
    return None


def run_specialist(
    model_dir: Path,
    test_dataset: Path,
    max_tokens: int = 256,
    gpu_memory_utilization: float = 0.90,
) -> list[Pair]:
    """Run each test example through the merged specialist via vLLM offline.

    Returns a list of dicts each containing:
        index, expected, actual, expected_len, actual_len
    """
    try:
        from vllm import LLM, SamplingParams
    except ImportError as e:
        raise RuntimeError(
            "vLLM is not installed. Install with: pip install vllm"
        ) from e

    records = load_test_dataset(test_dataset)
    model_dir_str = str(model_dir.resolve())
    if not (model_dir / "config.json").exists():
        raise FileNotFoundError(
            f"{model_dir}: no config.json — is this a merged model dir?"
        )

    LOG.info("loading specialist model", extra={
        "model_dir": model_dir_str,
        "test_count": len(records),
    })

    llm = LLM(
        model=model_dir_str,
        gpu_memory_utilization=gpu_memory_utilization,
        max_model_len=2048,  # Qwen2.5-1.5B default; adjust if needed
    )
    sampling_params = SamplingParams(
        temperature=0.0,
        max_tokens=max_tokens,
    )

    # Build conversation prompts. For each record, we feed all messages EXCEPT
    # the final assistant turn as the prompt, and compare the generated response
    # against that held-out assistant content.
    prompts: list[list[dict[str, str]]] = []
    expectations: list[str] = []
    for rec in records:
        msgs = rec["messages"]
        expected = _extract_expected(rec) or ""
        last_ast = _last_assistant_msg(msgs)
        # Build prompt: all messages except the final assistant turn, normalising
        # each message to {role, content} (stripping extra fields that trip vLLM).
        prompt_msgs: list[dict[str, str]] = []
        for msg in msgs:
            if msg is last_ast:
                break
            prompt_msgs.append({
                "role": str(msg.get("role", "user")),
                "content": str(msg.get("content", "")),
            })
        expectations.append(expected)
        prompts.append(prompt_msgs)

    LOG.info("running specialist inference", extra={"prompts": len(prompts)})
    outputs = llm.chat(prompts, sampling_params)

    pairs: list[Pair] = []
    for i, (output, expected) in enumerate(zip(outputs, expectations)):
        actual = output.outputs[0].text.strip() if output.outputs else ""
        pairs.append({
            "index": i,
            "expected": expected,
            "actual": actual,
            "expected_len": len(expected),
            "actual_len": len(actual),
        })
        LOG.debug("specialist pair", extra={
            "index": i,
            "expected_len": len(expected),
            "actual_len": len(actual),
        })

    LOG.info("specialist inference complete", extra={
        "pairs": len(pairs),
        "total_expected_chars": sum(p["expected_len"] for p in pairs),
        "total_actual_chars": sum(p["actual_len"] for p in pairs),
    })
    return pairs


def _last_assistant_msg(messages: list[dict[str, Any]]) -> dict[str, Any] | None:
    """Return the last assistant message in the list, or None."""
    for msg in reversed(messages):
        if msg.get("role") == "assistant":
            return msg
    return None


def check_only(test_dataset: Path, model_dir: Path | None = None) -> int:
    """Validate test dataset + model dir structure without loading vLLM."""
    try:
        records = load_test_dataset(test_dataset)
    except (FileNotFoundError, ValueError) as e:
        LOG.error("check-only dataset failed", extra={"error": str(e)})
        return 5

    if model_dir is not None:
        if not model_dir.exists():
            LOG.error("model dir not found", extra={"model_dir": str(model_dir)})
            return 1
        if not (model_dir / "config.json").exists():
            LOG.error("model dir missing config.json",
                      extra={"model_dir": str(model_dir)})
            return 1

    LOG.info("check-only passed", extra={
        "test_dataset": str(test_dataset),
        "test_count": len(records),
        "first_input_len": len(records[0]["messages"][0]["content"])
        if records else 0,
    })
    return 0
