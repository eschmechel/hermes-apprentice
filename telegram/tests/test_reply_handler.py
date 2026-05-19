"""Reply parsing, decision-marker writes, and end-to-end poll_once."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from apprentice_telegram import outbox, reply_handler, templates


def test_parse_reply_accepts_variants():
    assert reply_handler.parse_reply("train") == ("train", None)
    assert reply_handler.parse_reply("TRAIN  ") == ("train", None)
    assert reply_handler.parse_reply("train gc-abcd1234") == ("train", "gc-abcd1234")
    assert reply_handler.parse_reply("Details GC-AbCd1234 please") == ("details", "gc-abcd1234")
    assert reply_handler.parse_reply("skip gc-deadbeef") == ("skip", "gc-deadbeef")


def test_parse_reply_rejects_nonsense():
    assert reply_handler.parse_reply("") is None
    assert reply_handler.parse_reply("hello") is None
    assert reply_handler.parse_reply("trainz") is None      # word-boundary
    assert reply_handler.parse_reply("please train") is None  # must be at start


def test_extract_replies_filters_and_normalizes():
    updates = [
        {"update_id": 1, "message": {
            "chat": {"id": 42}, "message_id": 10,
            "from": {"id": 7}, "text": "train gc-aabbccdd",
        }},
        {"update_id": 2, "message": {
            "chat": {"id": 42}, "message_id": 11,
            "from": {"id": 7}, "text": "lol no",
        }},
        {"update_id": 3, "message": {
            "chat": {"id": 42}, "message_id": 12,
            "from": {"id": 7}, "text": "Skip",
        }},
        {"update_id": 4},  # missing message
    ]
    out = reply_handler.extract_replies(updates)
    assert [(r.action, r.cid, r.update_id) for r in out] == [
        ("train", "gc-aabbccdd", 1),
        ("skip", None, 3),
    ]


def test_handle_reply_writes_marker(tmp_path: Path):
    reply = reply_handler.Reply(
        action="train", cid="gc-12345678",
        chat_id=42, message_id=99, user_id=7,
        text="train gc-12345678", update_id=5,
    )
    decisions = tmp_path / "decisions"
    marker = reply_handler.handle_reply(
        reply, decisions_root=decisions, outbox_root=tmp_path / "outbox",
    )
    assert marker.exists()
    payload = json.loads(marker.read_text(encoding="utf-8"))
    assert payload["action"] == "train"
    assert payload["cid"] == "gc-12345678"
    assert payload["chat_id"] == 42
    assert payload["update_id"] == 5
    assert "train gc-12345678" == payload["raw_text"]


def test_handle_reply_without_cid_picks_latest_graduation(tmp_path: Path):
    outbox_root = tmp_path / "outbox"
    # Enqueue a graduation; its cid is derived from pattern_id + "" salt.
    outbox.enqueue("graduation", "abc-pat", "body", outbox_root=outbox_root)
    expected_cid = templates.correlation_id("abc-pat", salt="")
    reply = reply_handler.Reply(
        action="train", cid=None, chat_id=1, message_id=1, user_id=None,
        text="train", update_id=1,
    )
    marker = reply_handler.handle_reply(
        reply, decisions_root=tmp_path / "decisions", outbox_root=outbox_root,
    )
    payload = json.loads(marker.read_text(encoding="utf-8"))
    assert payload["cid"] == expected_cid


def test_handle_reply_without_cid_or_pending_uses_sentinel(tmp_path: Path):
    reply = reply_handler.Reply(
        action="skip", cid=None, chat_id=1, message_id=1, user_id=None,
        text="skip", update_id=1,
    )
    marker = reply_handler.handle_reply(
        reply, decisions_root=tmp_path / "decisions",
        outbox_root=tmp_path / "outbox-empty",
    )
    payload = json.loads(marker.read_text(encoding="utf-8"))
    assert payload["cid"] == "latest-none"


def test_handle_reply_details_enqueues_preview(tmp_path: Path):
    reply = reply_handler.Reply(
        action="details", cid="gc-12345678",
        chat_id=1, message_id=1, user_id=None,
        text="details gc-12345678", update_id=1,
    )
    outbox_root = tmp_path / "outbox"
    reply_handler.handle_reply(
        reply, decisions_root=tmp_path / "decisions", outbox_root=outbox_root,
    )
    pending = outbox.list_pending(outbox_root)
    assert len(pending) == 1
    body = pending[0].read_text(encoding="utf-8")
    assert "[gc-12345678]" in body
    assert "details requested" in body


def test_offset_roundtrip(tmp_path: Path):
    p = tmp_path / "subdir" / "offset"
    assert reply_handler.read_offset(p) == 0
    reply_handler.write_offset(127, p)
    assert reply_handler.read_offset(p) == 127


class _FakeResponse:
    def __init__(self, payload: dict):
        self._payload = payload

    def raise_for_status(self) -> None:
        pass

    def json(self) -> dict:
        return self._payload


def test_poll_once_writes_markers_and_advances_offset(tmp_path: Path):
    payloads: list[dict] = [{
        "ok": True,
        "result": [
            {"update_id": 100, "message": {
                "chat": {"id": 42}, "message_id": 1,
                "from": {"id": 7}, "text": "train gc-aaaaaaaa",
            }},
            {"update_id": 101, "message": {
                "chat": {"id": 42}, "message_id": 2,
                "from": {"id": 7}, "text": "noise",
            }},
            {"update_id": 102, "message": {
                "chat": {"id": 42}, "message_id": 3,
                "from": {"id": 7}, "text": "details gc-bbbbbbbb",
            }},
        ],
    }]

    captured: dict = {}

    def fake_http_get(url, params=None, timeout=None):
        captured["url"] = url
        captured["params"] = dict(params or {})
        captured["timeout"] = timeout
        return _FakeResponse(payloads.pop(0))

    markers = reply_handler.poll_once(
        bot_token="TEST-TOKEN-123",
        decisions_root=tmp_path / "decisions",
        outbox_root=tmp_path / "outbox",
        offset_path=tmp_path / "offset",
        http_get=fake_http_get,
    )
    # Two recognised replies (train, details) → two markers.
    assert len(markers) == 2
    # The url carries the bot token; allowed_updates limits to messages.
    assert "TEST-TOKEN-123" in captured["url"]
    assert captured["params"]["allowed_updates"] == '["message"]'
    # Offset advanced past every update we saw — even the noise one.
    assert reply_handler.read_offset(tmp_path / "offset") == 103
    # Details reply queued a preview message back to the outbox.
    pending = outbox.list_pending(tmp_path / "outbox")
    assert len(pending) == 1
    assert "gc-bbbbbbbb" in pending[0].read_text(encoding="utf-8")


def test_poll_once_with_no_updates_does_nothing(tmp_path: Path):
    def fake_http_get(url, params=None, timeout=None):
        return _FakeResponse({"ok": True, "result": []})

    markers = reply_handler.poll_once(
        bot_token="t", decisions_root=tmp_path / "d",
        outbox_root=tmp_path / "o", offset_path=tmp_path / "off",
        http_get=fake_http_get,
    )
    assert markers == []
    # Offset is not advanced (no updates to advance past).
    assert reply_handler.read_offset(tmp_path / "off") == 0


def test_get_updates_raises_on_not_ok():
    def fake_http_get(url, params=None, timeout=None):
        return _FakeResponse({"ok": False, "description": "bad token"})

    with pytest.raises(RuntimeError, match="not ok"):
        reply_handler.get_updates("t", http_get=fake_http_get)
