"""File-queue outbox consumed by the Hermes ``--deliver telegram`` cron.

Layout:

  <outbox_root>/
      <utc-ts>-<kind>-<pattern_id>.txt          ← pending
      processed/<utc-ts>-<kind>-<pattern_id>.txt ← acked

A producer (validator, instrumentation) calls :func:`enqueue` to drop a
rendered message into ``<outbox_root>/``. The dispatcher script
(``apprentice-telegram dispatch-one``) calls :func:`claim_oldest` to atomically
move one file into a per-dispatch claim slot, prints its contents to stdout
(so Hermes' ``--deliver telegram`` sends it), and on success calls
:func:`ack` to move the file under ``processed/``. On non-zero exit the file
is restored to ``<outbox_root>/`` for retry.

Atomic ops use ``os.rename`` (same filesystem). On crash mid-dispatch the
in-flight file sits in ``.inflight/`` and is recovered to pending by the next
:func:`claim_oldest` call (so we don't lose messages on cron restarts).
"""

from __future__ import annotations

import datetime
import logging
import os
import re
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

LOG = logging.getLogger("apprentice_telegram.outbox")

DEFAULT_OUTBOX_ROOT = Path.home() / ".apprentice" / "outbox"

_KINDS = ("graduation", "failure", "weekly")
_NAME_RE = re.compile(r"^([0-9]{8}T[0-9]{6}Z)-(graduation|failure|weekly)-([A-Za-z0-9._-]+)\.txt$")


def _utc_stamp() -> str:
    """Compact UTC stamp used in file names. Lexicographically sorts == time-sorts."""
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def _sanitize_id(pattern_id: str) -> str:
    """File-name-safe form of an arbitrary id."""
    return re.sub(r"[^A-Za-z0-9._-]+", "_", pattern_id) or "_"


def _ensure_dirs(root: Path) -> tuple[Path, Path, Path]:
    pending = root
    inflight = root / ".inflight"
    processed = root / "processed"
    for p in (pending, inflight, processed):
        p.mkdir(parents=True, exist_ok=True)
    return pending, inflight, processed


def enqueue(
    kind: str,
    pattern_id: str,
    body: str,
    *,
    outbox_root: Path | None = None,
) -> Path:
    """Atomically drop a message into the outbox. Returns the final path."""
    if kind not in _KINDS:
        raise ValueError(f"unknown kind {kind!r}; expected one of {_KINDS}")
    if not body.strip():
        raise ValueError("body must be non-empty")
    root = Path(outbox_root) if outbox_root else DEFAULT_OUTBOX_ROOT
    pending, _inflight, _processed = _ensure_dirs(root)
    stamp = _utc_stamp()
    name = f"{stamp}-{kind}-{_sanitize_id(pattern_id)}.txt"
    final_path = pending / name
    # tempfile in same dir + replace = atomic on POSIX.
    fd, tmp_name = tempfile.mkstemp(prefix=name + ".", suffix=".tmp", dir=pending)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(body if body.endswith("\n") else body + "\n")
        os.replace(tmp_name, final_path)
    except Exception:
        try:
            os.unlink(tmp_name)
        except FileNotFoundError:
            pass
        raise
    LOG.info("enqueued telegram message", extra={
        "kind": kind, "pattern_id": pattern_id, "path": str(final_path),
    })
    return final_path


def _recover_inflight(pending: Path, inflight: Path) -> int:
    """Move any stuck .inflight/* files back into pending. Returns count."""
    moved = 0
    for f in sorted(inflight.iterdir()):
        if not f.is_file():
            continue
        target = pending / f.name
        if target.exists():
            # Pending already has a fresher copy; drop the stuck one.
            f.unlink(missing_ok=True)
            continue
        os.replace(f, target)
        moved += 1
    if moved:
        LOG.warning("recovered stuck inflight messages", extra={"count": moved})
    return moved


@dataclass
class Claim:
    inflight_path: Path
    final_name: str
    body: str


def claim_oldest(outbox_root: Path | None = None) -> Claim | None:
    """Move the oldest pending file into .inflight/ and return its contents.

    Returns ``None`` if the outbox has nothing pending. The caller MUST call
    :func:`ack` (success) or :func:`requeue` (failure) when done.
    """
    root = Path(outbox_root) if outbox_root else DEFAULT_OUTBOX_ROOT
    pending, inflight, _processed = _ensure_dirs(root)
    _recover_inflight(pending, inflight)
    candidates = [p for p in sorted(pending.iterdir())
                  if p.is_file() and _NAME_RE.match(p.name)]
    if not candidates:
        return None
    src = candidates[0]
    dst = inflight / src.name
    os.replace(src, dst)
    body = dst.read_text(encoding="utf-8")
    return Claim(inflight_path=dst, final_name=src.name, body=body)


def ack(claim: Claim, outbox_root: Path | None = None) -> Path:
    """Mark a claim as successfully delivered. Moves it under processed/."""
    root = Path(outbox_root) if outbox_root else DEFAULT_OUTBOX_ROOT
    _pending, _inflight, processed = _ensure_dirs(root)
    target = processed / claim.final_name
    os.replace(claim.inflight_path, target)
    return target


def requeue(claim: Claim, outbox_root: Path | None = None) -> Path:
    """Mark a claim as failed. Moves it back to pending for a future retry."""
    root = Path(outbox_root) if outbox_root else DEFAULT_OUTBOX_ROOT
    pending, _inflight, _processed = _ensure_dirs(root)
    target = pending / claim.final_name
    os.replace(claim.inflight_path, target)
    return target


def list_pending(outbox_root: Path | None = None) -> list[Path]:
    """Return pending messages in dispatch order (oldest first)."""
    root = Path(outbox_root) if outbox_root else DEFAULT_OUTBOX_ROOT
    pending, _inflight, _processed = _ensure_dirs(root)
    return [p for p in sorted(pending.iterdir())
            if p.is_file() and _NAME_RE.match(p.name)]


def list_processed(outbox_root: Path | None = None) -> list[Path]:
    root = Path(outbox_root) if outbox_root else DEFAULT_OUTBOX_ROOT
    _p, _i, processed = _ensure_dirs(root)
    return sorted(processed.iterdir())


def parse_name(name: str) -> tuple[str, str, str] | None:
    """Return (utc_stamp, kind, pattern_id) parsed from a filename, or None."""
    m = _NAME_RE.match(name)
    if not m:
        return None
    return m.group(1), m.group(2), m.group(3)
