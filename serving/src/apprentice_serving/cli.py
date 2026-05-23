"""Serving CLI — ``apprentice-serve`` and ``apprentice-bench`` entry points.

``apprentice-serve``
    Resolves a model path (direct or via Go registry), builds a ``vllm serve``
    command, and executes it as a foreground subprocess.

``apprentice-bench``
    Sends N requests to a running vLLM endpoint and emits latency statistics.
"""

from __future__ import annotations

import argparse
import logging
import os
from pathlib import Path

from . import bench as bench_mod
from . import server
from .logging import setup_logging

LOG = logging.getLogger("apprentice_serving")


# ---------------------------------------------------------------------------
# apprentice-serve
# ---------------------------------------------------------------------------

def build_serve_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-serve",
        description="Launch a vLLM server for a merged specialist model.",
    )
    mg = p.add_mutually_exclusive_group(required=True)
    mg.add_argument("--pattern-id", help="Pattern ID for registry lookup.")
    mg.add_argument("--model-dir", help="Direct path to merged model directory.")
    p.add_argument("--port", type=int, default=8000, help="vLLM listen port (default: 8000).")
    p.add_argument("--host", default="0.0.0.0", help="vLLM bind address.")
    p.add_argument("--registry-url", default="http://localhost:8082",
                   help="Go registry-service base URL (default: http://localhost:8082).")
    p.add_argument("--gpu-memory-util", type=float, default=0.90,
                   help="vLLM GPU memory fraction (default: 0.90).")
    p.add_argument("--max-model-len", type=int, default=2048,
                   help="vLLM max context length (default: 2048).")
    p.add_argument("--enable-lora", action="store_true",
                   help="Serve --model-dir as a WARM BASE and hot-swap specialist "
                        "LoRA adapters at runtime (multi-LoRA residency).")
    p.add_argument("--max-loras", type=int, default=4,
                   help="Max LoRA adapters resident at once (default: 4).")
    p.add_argument("--max-lora-rank", type=int, default=16,
                   help="Max LoRA rank (must be >= the trained rank; default: 16).")
    p.add_argument("--check-only", action="store_true",
                   help="Validate args + resolve model path without launching.")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def run_serve(args: argparse.Namespace) -> int:
    model_path = server.resolve_model_path(
        pattern_id=args.pattern_id,
        model_dir=args.model_dir,
        registry_url=args.registry_url,
    )
    if args.enable_lora:
        # Required for the runtime load/unload admin endpoints the residency
        # manager drives.
        os.environ["VLLM_ALLOW_RUNTIME_LORA_UPDATING"] = "True"
    cmd = server.build_vllm_cmd(
        model_path,
        host=args.host,
        port=args.port,
        gpu_memory_utilization=args.gpu_memory_util,
        max_model_len=args.max_model_len,
        enable_lora=args.enable_lora,
        max_loras=args.max_loras,
        max_lora_rank=args.max_lora_rank,
    )
    return server.launch_server(cmd)


def main_serve(argv: list[str] | None = None) -> int:
    args = build_serve_parser().parse_args(argv)
    setup_logging(logging.DEBUG if args.verbose else logging.INFO)

    if args.check_only:
        return server.check_only(
            pattern_id=args.pattern_id,
            model_dir=args.model_dir,
            registry_url=args.registry_url,
            port=args.port,
            host=args.host,
            gpu_memory_utilization=args.gpu_memory_util,
            max_model_len=args.max_model_len,
            enable_lora=args.enable_lora,
            max_loras=args.max_loras,
            max_lora_rank=args.max_lora_rank,
        )

    return run_serve(args)


# ---------------------------------------------------------------------------
# apprentice-bench
# ---------------------------------------------------------------------------

def build_bench_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-bench",
        description="Benchmark latency of a vLLM chat completions endpoint.",
    )
    p.add_argument("--endpoint", required=True,
                   help="Full URL of the chat completions endpoint "
                        "(e.g. http://localhost:8000/v1/chat/completions).")
    p.add_argument("--dataset", required=True,
                   help="Path to test.jsonl.gz (gzipped Hermes JSONL).")
    p.add_argument("--requests", type=int, default=100,
                   help="Number of benchmark requests (default: 100).")
    p.add_argument("--concurrency", type=int, default=1,
                   help="Concurrent requests (default: 1, sequential).")
    p.add_argument("--max-tokens", type=int, default=256,
                   help="Max tokens per request (default: 256).")
    p.add_argument("--warmup", type=int, default=5,
                   help="Warmup requests excluded from stats (default: 5).")
    p.add_argument("--timeout", type=float, default=60.0,
                   help="Per-request timeout in seconds (default: 60).")
    p.add_argument("--json", action="store_true", dest="json_output",
                   help="Output stats as JSON instead of a human table.")
    p.add_argument("-v", "--verbose", action="store_true")
    return p


def run_bench(args: argparse.Namespace) -> int:
    prompts = bench_mod.load_prompts(Path(args.dataset), limit=args.requests)
    if not prompts:
        LOG.error("no prompts loaded from dataset")
        return 1

    LOG.info("starting benchmark", extra={
        "endpoint": args.endpoint,
        "requests": len(prompts),
        "warmup": args.warmup,
        "max_tokens": args.max_tokens,
    })

    results = bench_mod.run_benchmark(
        endpoint=args.endpoint,
        prompts=prompts,
        max_tokens=args.max_tokens,
        concurrency=args.concurrency,
        warmup=args.warmup,
        timeout=args.timeout,
    )

    stats = bench_mod.compute_stats(results)
    bench_mod.print_stats(stats, json_output=args.json_output)
    return 0 if stats["errors"] == 0 else 1


def main_bench(argv: list[str] | None = None) -> int:
    args = build_bench_parser().parse_args(argv)
    setup_logging(logging.DEBUG if args.verbose else logging.INFO)
    return run_bench(args)
