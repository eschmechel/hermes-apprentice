"""Latency benchmark (serving-03).

Sends N requests to a vLLM endpoint, measures wall-clock timing per request,
and computes p50 / p95 / p99 / mean / min / max.

Pure HTTP client — no vLLM import.  Pairs ``apprentice-bench`` CLI with the
``apprentice-serve`` launcher.
"""

from __future__ import annotations

import json
import logging
import math
import statistics
import sys
import time
from pathlib import Path
from typing import Any, Sequence

import httpx

LOG = logging.getLogger("apprentice_serving.bench")

Result = dict[str, Any]
Stats = dict[str, Any]


def load_prompts(path: Path, limit: int | None = None) -> list[str]:
    """Load prompts from a gzipped JSONL test dataset.

    Each line is ``{"messages": [{"role": ..., "content": ...}]}``.
    Returns the ``content`` of the **last user message** in each record.
    """
    import gzip

    prompts: list[str] = []
    with gzip.open(path, "rt", encoding="utf-8") as fp:
        for line in fp:
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            # Pick the last user message.
            user_text = ""
            for msg in reversed(rec.get("messages", [])):
                if msg.get("role") == "user":
                    user_text = msg.get("content", "")
                    break
            if user_text:
                prompts.append(user_text)
            if limit and len(prompts) >= limit:
                break
    return prompts


def run_benchmark(
    endpoint: str,
    prompts: list[str],
    *,
    max_tokens: int = 256,
    concurrency: int = 1,
    warmup: int = 0,
    timeout: float = 60.0,
) -> list[Result]:
    """Send each prompt to *endpoint* and record per-request timing.

    Returns a list of result dicts::

        {index, latency_ms, status_code, tokens, prompt_chars}
    """
    if not prompts:
        return []

    results: list[Result] = []

    # Warmup — not measured.
    for i in range(min(warmup, len(prompts))):
        LOG.debug("warmup request", extra={"index": i})
        try:
            _send_one(endpoint, prompts[i], max_tokens, timeout)
        except Exception:
            pass

    # Measured.
    for i, prompt in enumerate(prompts):
        LOG.debug("bench request", extra={"index": i, "concurrency": concurrency})
        t0 = time.monotonic()
        try:
            resp_data, status, tokens = _send_one(endpoint, prompt, max_tokens, timeout)
            elapsed = (time.monotonic() - t0) * 1000
            results.append({
                "index": i,
                "latency_ms": round(elapsed, 2),
                "status_code": status,
                "tokens": tokens,
                "prompt_chars": len(prompt),
            })
        except Exception as e:
            elapsed = (time.monotonic() - t0) * 1000
            results.append({
                "index": i,
                "latency_ms": round(elapsed, 2),
                "status_code": 0,
                "error": str(e),
                "tokens": 0,
                "prompt_chars": len(prompt),
            })

    return results


def compute_stats(results: list[Result]) -> Stats:
    """Compute aggregate statistics from benchmark results."""
    latencies = [r["latency_ms"] for r in results if r.get("status_code") == 200]
    errors = sum(1 for r in results if r.get("status_code") != 200)
    tokens = [r["tokens"] for r in results if r.get("status_code") == 200]

    if not latencies:
        return {
            "p50_ms": 0,
            "p95_ms": 0,
            "p99_ms": 0,
            "mean_ms": 0,
            "min_ms": 0,
            "max_ms": 0,
            "requests": len(results),
            "errors": errors,
            "tokens_total": 0,
            "tokens_mean": 0,
        }

    sorted_lat = sorted(latencies)
    n = len(sorted_lat)

    def percentile(p: float) -> float:
        k = (n - 1) * p
        f = math.floor(k)
        c = math.ceil(k)
        if f == c:
            return sorted_lat[int(k)]
        return sorted_lat[f] * (c - k) + sorted_lat[c] * (k - f)

    return {
        "p50_ms": round(percentile(0.50), 2),
        "p95_ms": round(percentile(0.95), 2),
        "p99_ms": round(percentile(0.99), 2),
        "mean_ms": round(statistics.mean(latencies), 2),
        "min_ms": round(min(latencies), 2),
        "max_ms": round(max(latencies), 2),
        "requests": len(results),
        "errors": errors,
        "tokens_total": sum(tokens),
        "tokens_mean": round(statistics.mean(tokens), 1) if tokens else 0,
    }


def print_stats(stats: Stats, json_output: bool = False) -> None:
    """Print stats to stdout — either human table or JSON."""
    if json_output:
        sys.stdout.write(json.dumps(stats, indent=2, sort_keys=True) + "\n")
        return

    sys.stdout.write(
        f"   Requests: {stats['requests']}  Errors: {stats['errors']}\n"
        f"   Tokens:   {stats['tokens_total']} total, {stats['tokens_mean']} avg\n"
        f"   p50:  {stats['p50_ms']:>8.1f} ms\n"
        f"   p95:  {stats['p95_ms']:>8.1f} ms\n"
        f"   p99:  {stats['p99_ms']:>8.1f} ms\n"
        f"   mean: {stats['mean_ms']:>8.1f} ms\n"
        f"   min:  {stats['min_ms']:>8.1f} ms\n"
        f"   max:  {stats['max_ms']:>8.1f} ms\n"
    )


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _send_one(
    endpoint: str,
    prompt: str,
    max_tokens: int,
    timeout: float,
) -> tuple[dict[str, Any], int, int]:
    """Send a single chat request and return (response_json, status_code, token_count)."""
    payload = {
        "model": "apprentice",
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
        "temperature": 0.0,
    }
    resp = httpx.post(
        endpoint,
        json=payload,
        timeout=httpx.Timeout(timeout, connect=5.0),
    )
    data = resp.json() if resp.text else {}
    usage = data.get("usage", {})
    tokens = usage.get("completion_tokens", 0) + usage.get("prompt_tokens", 0)
    return data, resp.status_code, tokens
