"""Incoming Telegram replies → on-disk decision markers.

Polls the Telegram Bot API (``getUpdates``) for replies starting with
``train`` / ``details`` / ``skip``, optionally followed by a correlation id
(``gc-abcd1234``). Each recognised reply produces a marker file under
``~/.apprentice/decisions/<utc>-<action>-<cid_or_latest>.json`` that the
training orchestrator picks up on its next tick.

If no correlation id is supplied with the reply, the handler resolves
"latest unhandled candidate" by looking at the most recent unacked
graduation message in the outbox.

Outbound *responses* (e.g. the dataset preview for ``details``) are
intentionally NOT sent from this script — that would duplicate Hermes'
delivery adapter, drift from its retry/escape behaviour, and require
shipping ``python-telegram-bot`` on the host. Instead, ``details`` queues a
*new* outbox entry (kind=graduation) carrying the preview text; the next
cron tick delivers it through Hermes like any other message.
"""

from __future__ import annotations

import datetime
import json
import logging
import os
import re
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Iterable

from . import outbox as outbox_mod
from . import templates

LOG = logging.getLogger("apprentice_telegram.replies")

DEFAULT_DECISIONS_ROOT = Path.home() / ".apprentice" / "decisions"
DEFAULT_OFFSET_PATH = Path.home() / ".apprentice" / "telegram" / "updates_offset"

_ACTIONS = ("train", "details", "skip")
# `train`, `train gc-abcd1234`, "Train GC-abcd1234", etc. Trailing punctuation
# / commentary is tolerated — we just want the action verb and an optional id.
_REPLY_RE = re.compile(
    r"^\s*(?P<action>train|details|skip)\b\s*(?P<cid>gc-[0-9a-f]{8})?",
    re.IGNORECASE,
)


@dataclass
class Reply:
    action: str            # "train" | "details" | "skip"
    cid: str | None        # correlation id, or None when omitted
    chat_id: int
    message_id: int
    user_id: int | None
    text: str
    update_id: int


def parse_reply(text: str) -> tuple[str, str | None] | None:
    """Return (action, cid_or_None) for a recognised reply, else None."""
    if not text:
        return None
    m = _REPLY_RE.match(text)
    if not m:
        return None
    action = m.group("action").lower()
    cid_raw = m.group("cid")
    cid = cid_raw.lower() if cid_raw else None
    return action, cid


def read_offset(offset_path: Path | None = None) -> int:
    """Read the next ``offset`` to pass to getUpdates. 0 = "from the start"."""
    path = Path(offset_path) if offset_path else DEFAULT_OFFSET_PATH
    if not path.exists():
        return 0
    try:
        return int(path.read_text(encoding="utf-8").strip() or "0")
    except (OSError, ValueError):
        return 0


def write_offset(offset: int, offset_path: Path | None = None) -> None:
    path = Path(offset_path) if offset_path else DEFAULT_OFFSET_PATH
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=path.name + ".", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(str(int(offset)))
        os.replace(tmp, path)
    except Exception:
        try:
            os.unlink(tmp)
        except FileNotFoundError:
            pass
        raise


def get_updates(
    bot_token: str,
    *,
    offset: int = 0,
    timeout: float = 5.0,
    http_get: Callable[..., Any] | None = None,
) -> list[dict]:
    """Call Telegram's ``getUpdates``. Returns the raw ``result`` array.

    ``http_get`` is injected for tests; production code uses ``httpx.get``.
    """
    if http_get is None:
        import httpx  # local import keeps unit tests off the network
        http_get = httpx.get
    url = f"https://api.telegram.org/bot{bot_token}/getUpdates"
    params = {"timeout": 0, "offset": offset, "allowed_updates": '["message"]'}
    resp = http_get(url, params=params, timeout=timeout)
    if hasattr(resp, "raise_for_status"):
        resp.raise_for_status()
    data = resp.json() if callable(getattr(resp, "json", None)) else {}
    if not data.get("ok", False):
        raise RuntimeError(f"telegram getUpdates not ok: {data!r}")
    return list(data.get("result", []))


def extract_replies(updates: Iterable[dict]) -> list[Reply]:
    """Filter raw Telegram updates into recognised Apprentice replies."""
    out: list[Reply] = []
    for u in updates:
        msg = u.get("message") or u.get("edited_message")
        if not msg:
            continue
        text = (msg.get("text") or "").strip()
        parsed = parse_reply(text)
        if parsed is None:
            continue
        action, cid = parsed
        out.append(Reply(
            action=action,
            cid=cid,
            chat_id=int(msg.get("chat", {}).get("id", 0)),
            message_id=int(msg.get("message_id", 0)),
            user_id=int(msg["from"]["id"]) if msg.get("from") else None,
            text=text,
            update_id=int(u.get("update_id", 0)),
        ))
    return out


def _latest_pending_graduation_cid(outbox_root: Path | None = None) -> str | None:
    """If a reply omits a cid, fall back to the most recent graduation in flight."""
    pending = outbox_mod.list_pending(outbox_root)
    # Iterate newest first.
    for path in reversed(pending):
        parsed = outbox_mod.parse_name(path.name)
        if parsed is None:
            continue
        _stamp, kind, pattern_id = parsed
        if kind != "graduation":
            continue
        # Derive cid from pattern_id with empty salt — same recipe used at enqueue time.
        return templates.correlation_id(pattern_id, salt="")
    return None


def _write_marker(
    *,
    action: str,
    cid: str,
    payload: dict,
    decisions_root: Path | None = None,
) -> Path:
    root = Path(decisions_root) if decisions_root else DEFAULT_DECISIONS_ROOT
    root.mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    name = f"{stamp}-{action}-{cid}.json"
    final = root / name
    body = json.dumps(payload, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    fd, tmp = tempfile.mkstemp(prefix=name + ".", suffix=".tmp", dir=root)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(body)
        os.replace(tmp, final)
    except Exception:
        try:
            os.unlink(tmp)
        except FileNotFoundError:
            pass
        raise
    return final


def _enqueue_details_preview(reply: Reply, cid: str, outbox_root: Path | None) -> Path | None:
    """For ``details`` replies, queue a preview message back to Telegram.

    Looks for an existing dataset preview file at
    ``~/.apprentice/datasets/<pattern_id>/v*/preview.txt``. If none is found,
    emits a placeholder pointing the user to the next action.
    """
    # Without the original pattern_id we can't open the preview directly, so
    # we emit a short note keyed by cid. The orchestrator that produced the
    # graduation message is expected to write a preview file at enqueue time;
    # see notes/telegram-integration.md.
    note = (
        f"[{cid}] details requested.\n\n"
        f"A dataset preview will be queued by the next cron tick. "
        f"If nothing arrives in ~2 minutes the preview file is missing — "
        f"check ~/.apprentice/datasets/.\n"
    )
    try:
        return outbox_mod.enqueue("graduation", cid, note, outbox_root=outbox_root)
    except (OSError, ValueError):
        LOG.exception("failed to enqueue details preview")
        return None


def handle_reply(
    reply: Reply,
    *,
    decisions_root: Path | None = None,
    outbox_root: Path | None = None,
) -> Path:
    """Write the decision marker for ``reply``. Returns the marker path.

    For ``details``: also enqueues a preview-placeholder message back into
    the outbox so the user gets feedback on the next tick.
    """
    cid = reply.cid or _latest_pending_graduation_cid(outbox_root)
    if cid is None:
        cid = "latest-none"  # explicit sentinel so the marker is still useful for debugging
    payload = {
        "action": reply.action,
        "cid": cid,
        "chat_id": reply.chat_id,
        "message_id": reply.message_id,
        "user_id": reply.user_id,
        "update_id": reply.update_id,
        "raw_text": reply.text,
        "received_at": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
    }
    marker = _write_marker(
        action=reply.action, cid=cid, payload=payload, decisions_root=decisions_root,
    )
    if reply.action == "details":
        _enqueue_details_preview(reply, cid, outbox_root)
    LOG.info("decision marker written", extra={
        "action": reply.action, "cid": cid, "marker": str(marker),
    })
    return marker


def poll_once(
    *,
    bot_token: str,
    decisions_root: Path | None = None,
    outbox_root: Path | None = None,
    offset_path: Path | None = None,
    http_get: Callable[..., Any] | None = None,
) -> list[Path]:
    """Single poll cycle: fetch updates, handle replies, advance offset.

    Returns the list of marker paths written this cycle (possibly empty).
    """
    offset = read_offset(offset_path)
    updates = get_updates(bot_token, offset=offset, http_get=http_get)
    if not updates:
        return []
    replies = extract_replies(updates)
    markers: list[Path] = []
    for r in replies:
        try:
            markers.append(handle_reply(
                r, decisions_root=decisions_root, outbox_root=outbox_root,
            ))
        except OSError:
            LOG.exception("marker write failed", extra={"update_id": r.update_id})
    # Always advance the offset past every update we observed — even ones we
    # didn't recognise — so the next poll doesn't re-fetch noise. Telegram
    # convention: offset = last_update_id + 1.
    new_offset = max(int(u.get("update_id", 0)) for u in updates) + 1
    write_offset(new_offset, offset_path)
    return markers
