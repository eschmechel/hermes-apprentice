"""Placement policy / VRAM arbiter (W3).

Warm multi-LoRA serving (~3 GB resident base) and local QLoRA training (~3-4 GB)
can't coexist on one small GPU. Before a local training run the orchestrator
checks free VRAM and, if the warm serve is holding it, applies
``on_vram_conflict``:

* ``evict``  — stop the warm serve to free the GPU (the proxy degrades to
               upstream meanwhile; restart is W9 / the installer's restart policy).
* ``cloud``  — burst to RunPod (stretch; the burst path is an untested TODO).
* ``skip``   — proceed and let training OOM (not recommended).

``free_vram_mb`` and ``_stop_warm_serve`` are module-level so tests can
monkeypatch them without touching a real GPU.
"""

from __future__ import annotations

import logging
import subprocess
import time

from .config import Config

LOG = logging.getLogger("apprentice_orchestrator.placement")

_SERVE_PATTERNS = ("apprentice-serve-control", "apprentice-serve", "vllm serve")

# How long to wait for the warm serve to actually release VRAM after pkill
# (pkill is async; vLLM takes a few seconds to tear down CUDA context).
_EVICT_TIMEOUT_S = 30.0
_EVICT_POLL_S = 1.0


def free_vram_mb() -> int | None:
    """Free VRAM on GPU 0 in MiB, or None if nvidia-smi is unavailable."""
    try:
        out = subprocess.run(
            ["nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits"],
            capture_output=True, text=True, timeout=10,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return None
    if out.returncode != 0:
        return None
    try:
        return int(out.stdout.strip().splitlines()[0].strip())
    except (ValueError, IndexError):
        return None


def _stop_warm_serve() -> None:
    for pat in _SERVE_PATTERNS:
        subprocess.run(["pkill", "-f", pat], capture_output=True)


def _wait_for_vram(need_mb: int, *, timeout: float | None = None,
                   poll: float | None = None) -> int | None:
    """Block until free VRAM >= need_mb (or timeout). Returns the last reading.

    pkill returns before vLLM has released its CUDA context, so a train launched
    immediately after eviction can still OOM. Poll until the GPU is actually free.
    Timeout/poll resolve from module constants at call time (monkeypatchable).
    """
    timeout = _EVICT_TIMEOUT_S if timeout is None else timeout
    poll = _EVICT_POLL_S if poll is None else poll
    deadline = time.monotonic() + timeout
    free = free_vram_mb()
    while free is not None and free < need_mb and time.monotonic() < deadline:
        time.sleep(poll)
        free = free_vram_mb()
    return free


def decide(cfg: Config) -> str:
    """Resolve the placement for an autonomous run."""
    if cfg.training_placement == "cloud":
        from .flash_burst import can_burst
        tenant = getattr(cfg, "tenant_id", None) or "default"
        decision = can_burst(cfg, tenant, cfg.burst_gpu)
        if decision["allowed"]:
            return "cloud"
        LOG.warning("cloud placement requested but burst blocked (%d reason(s))",
                    len(decision.get("reasons", [])))
    return "local"


def prepare_local_gpu(cfg: Config) -> dict:
    """Make room for a local training run. Returns a report dict (also recorded
    on the job). No-op when nvidia-smi is unavailable or enough VRAM is free."""
    free = free_vram_mb()
    need = cfg.train_vram_mb
    if free is None:
        return {"placement": "local", "vram_checked": False}
    if free >= need:
        return {"placement": "local", "free_mb": free, "need_mb": need, "evicted": False}
    LOG.warning("insufficient VRAM for local train", extra={"free_mb": free, "need_mb": need,
                                                            "policy": cfg.on_vram_conflict})
    if cfg.on_vram_conflict == "evict":
        _stop_warm_serve()
        freed = _wait_for_vram(need)
        if freed is not None and freed < need:
            LOG.warning("evicted warm serve but VRAM still below need; training may OOM",
                        extra={"free_mb": freed, "need_mb": need})
        return {"placement": "local", "free_mb": freed, "need_mb": need,
                "evicted": True, "reason": "vram_conflict"}
    return {"placement": "local", "free_mb": free, "need_mb": need,
            "evicted": False, "reason": "vram_conflict_unhandled"}
