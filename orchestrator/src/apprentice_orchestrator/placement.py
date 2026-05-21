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

from .config import Config

LOG = logging.getLogger("apprentice_orchestrator.placement")

_SERVE_PATTERNS = ("apprentice-serve-control", "apprentice-serve", "vllm serve")


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


def decide(cfg: Config) -> str:
    """Resolve the placement for an autonomous run. 'cloud' is not yet wired
    (burst untested), so it falls back to local with a warning."""
    if cfg.training_placement == "cloud":
        LOG.warning("cloud placement requested but burst path is not yet wired "
                    "(stretch/untested); running local")
        return "local"
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
        return {"placement": "local", "free_mb": free, "need_mb": need,
                "evicted": True, "reason": "vram_conflict"}
    return {"placement": "local", "free_mb": free, "need_mb": need,
            "evicted": False, "reason": "vram_conflict_unhandled"}
