"""Graduation notifier — the seam the detector (or a cron) calls when a pattern
is approved. Enqueues the Telegram graduation message AND records the
cid→pattern mapping so the operator's ``train gc-…`` reply can be resolved.
"""

from __future__ import annotations

import logging
import shutil
import subprocess
import sys
from pathlib import Path

from . import candidates
from .config import Config

LOG = logging.getLogger("apprentice_orchestrator.notify")


def _telegram_bin() -> str | None:
    cand = Path(sys.executable).parent / "apprentice-telegram"
    return str(cand) if cand.exists() else shutil.which("apprentice-telegram")


def notify(
    cfg: Config,
    pattern_id: str,
    *,
    record_count: int,
    description: str,
    examples: list[str] | None = None,
    salt: str = "",
) -> str:
    """Record the candidate (cid→pattern) and enqueue the graduation message.

    Returns the cid. The cid uses the same (pattern_id, salt) the message will,
    so the index and the delivered ``[gc-…]`` line agree.
    """
    cid = candidates.write(cfg, pattern_id, salt=salt)

    tg = _telegram_bin()
    if not tg:
        LOG.warning("apprentice-telegram not found; candidate recorded but not enqueued",
                    extra={"cid": cid, "pattern_id": pattern_id})
        return cid

    argv = [tg, "enqueue", "graduation",
            "--pattern-id", pattern_id,
            "--record-count", str(record_count),
            "--description", description,
            "--outbox-root", str(cfg.outbox_dir)]
    for ex in (examples or []):
        argv += ["--example", ex]
    proc = subprocess.run(argv, capture_output=True, text=True)
    if proc.returncode != 0:
        LOG.error("graduation enqueue failed", extra={"error": proc.stderr[-300:]})
    else:
        LOG.info("graduation enqueued", extra={"cid": cid, "pattern_id": pattern_id})
    return cid
