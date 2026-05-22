"""Baseline runner (validator-02).

Runs the same test set through the raw Qwen2.5-1.5B-Instruct model (not the
specialist) using vLLM offline batch API and collects (expected, actual)
pairs in the same shape as ``test_runner`` so metrics can compare directly.

The prompt for each record is the same message list given to the specialist,
truncated to exclude the final assistant turn (the held-out answer).
"""

from __future__ import annotations

import logging
from pathlib import Path
from typing import Any

from apprentice_trainer import models

from . import test_runner

LOG = logging.getLogger("apprentice_validator.baseline")

DEFAULT_BASE_MODEL = models.get_default_model()

Pair = dict[str, Any]


def run_baseline(
    test_dataset: Path,
    base_model: str = DEFAULT_BASE_MODEL,
    max_tokens: int = 256,
    gpu_memory_utilization: float = 0.90,
) -> list[Pair]:
    """Run each test example through the raw base model via vLLM offline.

    Returns a list of dicts matching the specialist output format:
        index, expected, actual, expected_len, actual_len
    """
    try:
        from vllm import LLM, SamplingParams
    except ImportError as e:
        raise RuntimeError(
            "vLLM is not installed. Install with: pip install vllm"
        ) from e

    records = test_runner.load_test_dataset(test_dataset)

    LOG.info("loading baseline model", extra={
        "base_model": base_model,
        "test_count": len(records),
    })

    llm = LLM(
        model=base_model,
        gpu_memory_utilization=gpu_memory_utilization,
        max_model_len=2048,
    )
    sampling_params = SamplingParams(
        temperature=0.0,
        max_tokens=max_tokens,
    )

    # Build conversations. The prompt includes all messages up to (but not
    # including) the last assistant turn (the held-out answer).
    prompts: list[list[dict[str, str]]] = []
    expectations: list[str] = []
    for rec in records:
        msgs = rec["messages"]
        expected = test_runner._extract_expected(rec) or ""
        # Build prompt: system msg (if present) + non-final-assistant turns.
        prompt_msgs: list[dict[str, str]] = []
        for msg in msgs:
            if msg.get("role") == "assistant" and _is_last_assistant(msgs, msg):
                break  # stop before the held-out answer
            prompt_msgs.append(_normalize_message(msg))
        expectations.append(expected)
        prompts.append(prompt_msgs)

    LOG.info("running baseline inference", extra={"prompts": len(prompts)})
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
        LOG.debug("baseline pair", extra={
            "index": i,
            "expected_len": len(expected),
            "actual_len": len(actual),
        })

    LOG.info("baseline inference complete", extra={
        "pairs": len(pairs),
        "total_expected_chars": sum(p["expected_len"] for p in pairs),
        "total_actual_chars": sum(p["actual_len"] for p in pairs),
    })
    return pairs


def _is_last_assistant(messages: list[dict[str, Any]], target: dict[str, Any]) -> bool:
    """Return True if *target* is the last assistant message in the list."""
    last = None
    for msg in messages:
        if msg.get("role") == "assistant":
            last = msg
    return last is target if last is not None else False


def _normalize_message(msg: dict[str, Any]) -> dict[str, str]:
    """Ensure message has only {'role': str, 'content': str} for vLLM chat."""
    return {
        "role": str(msg.get("role", "user")),
        "content": str(msg.get("content", "")),
    }
