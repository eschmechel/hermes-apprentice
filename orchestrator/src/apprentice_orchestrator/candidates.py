"""Candidate index — maps a Telegram correlation id back to a pattern.

The graduation cid ``gc-<8hex>`` is ``sha256(pattern_id, salt)`` (see
``apprentice_telegram.templates.correlation_id``) and is therefore NOT
reversible. So at graduation time we record the mapping at
``~/.apprentice/candidates/<cid>.json``; the watcher reads it to turn a
``train gc-…`` decision marker back into a pattern_id.
"""

from __future__ import annotations

import json
import time
from pathlib import Path

from .config import Config


def compute_cid(pattern_id: str, salt: str = "") -> str:
    # Reuse the telegram implementation so the cid always matches the message.
    from apprentice_telegram.templates import correlation_id

    return correlation_id(pattern_id, salt=salt)


def write(cfg: Config, pattern_id: str, *, salt: str = "", dataset_dir: str | None = None) -> str:
    """Record the cid→pattern mapping; return the cid."""
    cid = compute_cid(pattern_id, salt)
    cfg.candidates_dir.mkdir(parents=True, exist_ok=True)
    record = {
        "cid": cid,
        "pattern_id": pattern_id,
        "salt": salt,
        "dataset_dir": dataset_dir,
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    (cfg.candidates_dir / f"{cid}.json").write_text(json.dumps(record, indent=2), encoding="utf-8")
    return cid


def resolve(cfg: Config, cid: str) -> str | None:
    """cid → pattern_id. Prefer the index; fall back to recomputing over the
    detector's known patterns (covers an index that was never written)."""
    rec = cfg.candidates_dir / f"{cid}.json"
    if rec.exists():
        try:
            return json.loads(rec.read_text(encoding="utf-8")).get("pattern_id")
        except (json.JSONDecodeError, OSError):
            pass
    # Fallback: brute-force match against patterns/<id>/manifest.json ids.
    if cfg.patterns_dir.is_dir():
        for manifest in cfg.patterns_dir.glob("*/manifest.json"):
            pid = manifest.parent.name
            try:
                if compute_cid(pid) == cid:
                    return pid
            except Exception:
                continue
    return None
