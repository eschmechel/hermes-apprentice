"""vLLM server launcher (serving-01).

Resolves the model path from either a direct ``--model-dir`` argument or an
``--pattern-id`` registry lookup against the Go registry-service, then shells
out to ``vllm serve`` as a foreground subprocess.

Design:
    No vLLM import — subprocess only.  This keeps the validator's offline
    import path clean and avoids fighting vLLM's async event loop.
"""

from __future__ import annotations

import json
import logging
import os
import shlex
import signal
import subprocess
import sys
from pathlib import Path
from typing import Any

import httpx

LOG = logging.getLogger("apprentice_serving.server")


# ---------------------------------------------------------------------------
# Registry lookup
# ---------------------------------------------------------------------------

def resolve_model_path(
    *,
    pattern_id: str | None = None,
    model_dir: str | None = None,
    registry_url: str = "http://localhost:8082",
    timeout: float = 5.0,
) -> Path:
    """Return the absolute path to a merged model.

    Exactly one of *pattern_id* or *model_dir* must be provided.
    """
    if pattern_id and model_dir:
        raise ValueError("specify --pattern-id or --model-dir, not both")
    if not pattern_id and not model_dir:
        raise ValueError("specify --pattern-id or --model-dir")

    if model_dir:
        path = Path(model_dir).expanduser().resolve()
    else:
        url = f"{registry_url.rstrip('/')}/registry/{pattern_id}/latest"
        LOG.info("querying registry", extra={"url": url})
        try:
            resp = httpx.get(url, timeout=timeout)
            resp.raise_for_status()
        except httpx.HTTPError as e:
            raise RuntimeError(
                f"registry lookup failed ({url}): {e}"
            ) from e

        data = resp.json()
        if not data.get("found"):
            raise RuntimeError(
                f"pattern '{pattern_id}' not found in registry at {registry_url}"
            )

        manifest = data["manifest"]
        if isinstance(manifest, str):
            manifest = json.loads(manifest)
        path = Path(manifest["model_dir"]).expanduser().resolve()

    if not path.exists():
        raise FileNotFoundError(f"model dir not found: {path}")
    if not (path / "config.json").exists():
        raise FileNotFoundError(f"{path}: no config.json — is this a merged model?")
    return path


# ---------------------------------------------------------------------------
# vLLM subprocess launcher
# ---------------------------------------------------------------------------

def build_vllm_cmd(
    model_path: Path,
    *,
    host: str = "0.0.0.0",
    port: int = 8000,
    gpu_memory_utilization: float = 0.90,
    max_model_len: int = 2048,
) -> list[str]:
    """Return the argv list for ``vllm serve``."""
    return [
        "vllm", "serve", str(model_path),
        "--host", host,
        "--port", str(port),
        "--gpu-memory-utilization", str(gpu_memory_utilization),
        "--max-model-len", str(max_model_len),
    ]


def launch_server(cmd: list[str]) -> int:
    """Run *cmd* as a foreground subprocess, forwarding SIGINT/SIGTERM, and
    block until it exits.  Returns the subprocess exit code.
    """
    LOG.info("launching vLLM", extra={"cmd": shlex.join(cmd)})
    proc = subprocess.Popen(
        cmd,
        stdout=sys.stdout,
        stderr=sys.stderr,
        # Put vLLM in its own process group so the terminal's Ctrl+C
        # (delivered to the foreground pgrp) only reaches the launcher.
        # We then forward the signal explicitly below — this lets the launcher
        # log the shutdown and clean up its own state before vLLM dies.
        preexec_fn=os.setpgrp if sys.platform != "win32" else None,
    )

    def forward_signal(signum: int, _frame: Any) -> None:
        LOG.info("received signal", extra={"signal": signum})
        proc.send_signal(signum)

    orig_sigint = signal.signal(signal.SIGINT, forward_signal)
    orig_sigterm = signal.signal(signal.SIGTERM, forward_signal)
    try:
        return proc.wait()
    finally:
        signal.signal(signal.SIGINT, orig_sigint)
        signal.signal(signal.SIGTERM, orig_sigterm)


# ---------------------------------------------------------------------------
# Check-only (CPU-safe, no subprocess)
# ---------------------------------------------------------------------------

def check_only(
    *,
    pattern_id: str | None = None,
    model_dir: str | None = None,
    registry_url: str = "http://localhost:8082",
    port: int = 8000,
    host: str = "0.0.0.0",
    gpu_memory_utilization: float = 0.90,
    max_model_len: int = 2048,
) -> int:
    """Validate arguments without launching vLLM.  Returns exit code."""
    try:
        path = resolve_model_path(
            pattern_id=pattern_id,
            model_dir=model_dir,
            registry_url=registry_url,
        )
    except (RuntimeError, FileNotFoundError, ValueError) as e:
        LOG.error("check-only failed", extra={"error": str(e)})
        return 1

    cmd = build_vllm_cmd(
        path,
        host=host,
        port=port,
        gpu_memory_utilization=gpu_memory_utilization,
        max_model_len=max_model_len,
    )

    LOG.info("check-only passed", extra={
        "model_path": str(path),
        "cmd": shlex.join(cmd),
        "vllm_binary": "vllm",  # checked at launch time
    })
    return 0
