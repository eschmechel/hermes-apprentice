"""Outbox file queue: enqueue → claim → ack/requeue + inflight recovery."""

from __future__ import annotations

import os
import time
from pathlib import Path

import pytest

from apprentice_telegram import outbox


def test_enqueue_rejects_empty_body(tmp_path: Path):
    with pytest.raises(ValueError, match="non-empty"):
        outbox.enqueue("graduation", "p", "   \n  ", outbox_root=tmp_path)


def test_enqueue_rejects_unknown_kind(tmp_path: Path):
    with pytest.raises(ValueError, match="unknown kind"):
        outbox.enqueue("nope", "p", "body", outbox_root=tmp_path)


def test_enqueue_writes_atomically_and_appends_newline(tmp_path: Path):
    path = outbox.enqueue("graduation", "abc-123", "hello world",
                          outbox_root=tmp_path)
    assert path.exists()
    assert path.read_text(encoding="utf-8") == "hello world\n"
    # No stray .tmp files.
    leftovers = [p for p in tmp_path.iterdir() if p.suffix == ".tmp"]
    assert leftovers == []


def test_enqueue_sanitizes_pattern_id_in_filename(tmp_path: Path):
    path = outbox.enqueue("failure", "weird id with spaces!", "body",
                          outbox_root=tmp_path)
    parsed = outbox.parse_name(path.name)
    assert parsed is not None
    _stamp, kind, pid = parsed
    assert kind == "failure"
    assert " " not in pid and "!" not in pid


def test_claim_oldest_returns_none_on_empty(tmp_path: Path):
    assert outbox.claim_oldest(outbox_root=tmp_path) is None


def test_claim_then_ack_moves_to_processed(tmp_path: Path):
    path = outbox.enqueue("graduation", "p", "first", outbox_root=tmp_path)
    claim = outbox.claim_oldest(outbox_root=tmp_path)
    assert claim is not None
    assert claim.body == "first\n"
    # Source no longer in pending.
    assert not path.exists()
    # In-flight file exists.
    assert claim.inflight_path.exists()
    # Ack moves it to processed/.
    final = outbox.ack(claim, outbox_root=tmp_path)
    assert final.exists()
    assert final.parent.name == "processed"
    # No leftover .inflight file.
    assert not claim.inflight_path.exists()


def test_claim_then_requeue_returns_to_pending(tmp_path: Path):
    outbox.enqueue("graduation", "p", "first", outbox_root=tmp_path)
    claim = outbox.claim_oldest(outbox_root=tmp_path)
    assert claim is not None
    out = outbox.requeue(claim, outbox_root=tmp_path)
    assert out.parent == tmp_path
    assert out.exists()
    # And it's claimable again next time.
    again = outbox.claim_oldest(outbox_root=tmp_path)
    assert again is not None
    assert again.body == "first\n"


def test_oldest_dispatched_first(tmp_path: Path):
    # Stamps include only second resolution, so write three messages with
    # explicit different stamps in the filename by sleeping >1s OR by
    # directly creating files with controlled names.
    msgs = []
    for i in range(3):
        msgs.append(outbox.enqueue("graduation", f"p{i}", f"body{i}",
                                   outbox_root=tmp_path))
        time.sleep(1.01)
    claim = outbox.claim_oldest(outbox_root=tmp_path)
    assert claim is not None
    assert claim.body == "body0\n"
    outbox.ack(claim, outbox_root=tmp_path)

    claim2 = outbox.claim_oldest(outbox_root=tmp_path)
    assert claim2.body == "body1\n"


def test_inflight_recovery_on_next_claim(tmp_path: Path):
    """A stuck .inflight/* file (cron crash) is restored to pending."""
    pending, inflight, _processed = outbox._ensure_dirs(tmp_path)
    # Simulate a crashed-mid-dispatch claim.
    stuck = inflight / "20260519T000000Z-graduation-stuck.txt"
    stuck.write_text("stuck-body\n", encoding="utf-8")
    # Next claim recovers it.
    claim = outbox.claim_oldest(outbox_root=tmp_path)
    assert claim is not None
    assert claim.body == "stuck-body\n"
    assert claim.final_name == stuck.name


def test_inflight_recovery_drops_duplicate(tmp_path: Path):
    """If both pending/X and .inflight/X exist, drop the stuck inflight copy."""
    pending, inflight, _processed = outbox._ensure_dirs(tmp_path)
    name = "20260519T000000Z-graduation-dup.txt"
    (pending / name).write_text("fresh\n", encoding="utf-8")
    (inflight / name).write_text("stuck\n", encoding="utf-8")
    # Drive recovery directly so we can assert its post-state without the
    # subsequent claim re-populating .inflight from pending.
    outbox._recover_inflight(pending, inflight)
    assert not (inflight / name).exists()
    # And the pending file is the fresh copy, untouched.
    assert (pending / name).read_text(encoding="utf-8") == "fresh\n"


def test_parse_name_rejects_garbage(tmp_path: Path):
    assert outbox.parse_name("not-a-real-name.txt") is None
    assert outbox.parse_name("20260519T000000Z-graduation-foo.txt") == (
        "20260519T000000Z", "graduation", "foo",
    )


def test_list_pending_and_processed(tmp_path: Path):
    outbox.enqueue("graduation", "p1", "a", outbox_root=tmp_path)
    time.sleep(1.01)
    outbox.enqueue("failure", "p2", "b", outbox_root=tmp_path)
    pending = outbox.list_pending(outbox_root=tmp_path)
    assert len(pending) == 2
    # Ack the oldest.
    claim = outbox.claim_oldest(outbox_root=tmp_path)
    outbox.ack(claim, outbox_root=tmp_path)
    assert len(outbox.list_pending(outbox_root=tmp_path)) == 1
    assert len(outbox.list_processed(outbox_root=tmp_path)) == 1
