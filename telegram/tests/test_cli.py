"""CLI smoke tests — enqueue, dispatch-one, list. No network calls."""

from __future__ import annotations

import io
import json
import sys
from pathlib import Path

import pytest

from apprentice_telegram import cli, outbox


def _run(args: list[str], capsys, stdin: str | None = None) -> tuple[int, str, str]:
    if stdin is not None:
        sys.stdin = io.StringIO(stdin)
    try:
        rc = cli.main(args)
    finally:
        sys.stdin = sys.__stdin__
    captured = capsys.readouterr()
    return rc, captured.out, captured.err


def test_enqueue_graduation_creates_outbox_file(tmp_path: Path, capsys):
    rc, out, _ = _run([
        "enqueue", "graduation",
        "--pattern-id", "abc-123",
        "--record-count", "42",
        "--description", "Extract SKU + qty.",
        "--example", "Confirm SKU AX-7 qty 3.",
        "--outbox-root", str(tmp_path),
    ], capsys)
    assert rc == 0
    payload = json.loads(out)
    assert payload["enqueued"].endswith(".txt")
    path = Path(payload["enqueued"])
    assert path.exists()
    body = path.read_text(encoding="utf-8")
    assert "42 similar requests" in body
    assert payload["cid"].startswith("gc-")
    assert f"train {payload['cid']}" in body


def test_enqueue_failure_reads_validator_result(tmp_path: Path, capsys):
    result_path = tmp_path / "result.json"
    result_path.write_text(json.dumps({
        "pattern_id": "abc-123",
        "specialist_scores": {"exact_match": 0.50, "f1": 0.55},
        "baseline_scores": {"exact_match": 0.48, "f1": 0.52},
        "verdict": {"reason": "specialist did not clear margin"},
    }))
    rc, out, _ = _run([
        "enqueue", "failure",
        "--validator-result", str(result_path),
        "--outbox-root", str(tmp_path),
    ], capsys)
    assert rc == 0
    body = Path(json.loads(out)["enqueued"]).read_text(encoding="utf-8")
    assert "abc-123" in body
    assert "50.0%" in body
    assert "did not clear margin" in body


def test_enqueue_weekly_reads_summary(tmp_path: Path, capsys):
    summary_path = tmp_path / "summary.json"
    summary_path.write_text(json.dumps({
        "window_start": "2026-05-12T00:00:00Z",
        "window_end":   "2026-05-19T00:00:00Z",
        "totals": {
            "total_requests": 10, "served_locally": 7,
            "escalated_to_upstream": 3, "total_cost_usd": 0.5,
            "total_cost_saved_usd": 1.5, "seconds_saved": 12.0,
        },
        "per_pattern": [
            {"pattern_id": "x", "volume": 7, "avg_latency_ms": 100.0,
             "cost_saved_usd": 1.0},
        ],
    }))
    rc, out, _ = _run([
        "enqueue", "weekly",
        "--summary-json", str(summary_path),
        "--outbox-root", str(tmp_path),
    ], capsys)
    assert rc == 0
    path = Path(json.loads(out)["enqueued"])
    body = path.read_text(encoding="utf-8")
    assert "Apprentice weekly" in body
    assert "2026-05-12" in body


def test_enqueue_raw_uses_stdin(tmp_path: Path, capsys):
    rc, out, _ = _run([
        "enqueue", "raw",
        "--kind", "failure",
        "--pattern-id", "p",
        "--outbox-root", str(tmp_path),
    ], capsys, stdin="hello from stdin\n")
    assert rc == 0
    body = Path(json.loads(out)["enqueued"]).read_text(encoding="utf-8")
    assert body == "hello from stdin\n"


def test_dispatch_one_pops_and_acks(tmp_path: Path, capsys):
    import time
    outbox.enqueue("graduation", "p", "first body\n", outbox_root=tmp_path)
    # Stamps have one-second resolution, so the second enqueue needs to land
    # at least one second later for dispatch-order assertions to be stable.
    time.sleep(1.01)
    outbox.enqueue("failure", "p", "second body\n", outbox_root=tmp_path)
    # First dispatch: prints first body, leaves second in pending.
    rc, out, _ = _run(
        ["dispatch-one", "--outbox-root", str(tmp_path)], capsys,
    )
    assert rc == 0
    assert out == "first body\n"
    assert len(outbox.list_pending(tmp_path)) == 1
    assert len(outbox.list_processed(tmp_path)) == 1


def test_dispatch_one_empty_outbox_silent_success(tmp_path: Path, capsys):
    rc, out, _ = _run(
        ["dispatch-one", "--outbox-root", str(tmp_path)], capsys,
    )
    assert rc == 0
    assert out == ""  # Hermes treats empty stdout as a silent tick.


def test_list_pending_and_processed(tmp_path: Path, capsys):
    outbox.enqueue("graduation", "p", "a\n", outbox_root=tmp_path)
    rc, out, _ = _run(
        ["list", "--outbox-root", str(tmp_path), "--state", "pending"], capsys,
    )
    assert rc == 0
    assert ".txt" in out
    # Dispatch and check processed.
    _run(["dispatch-one", "--outbox-root", str(tmp_path)], capsys)
    rc2, out2, _ = _run(
        ["list", "--outbox-root", str(tmp_path), "--state", "processed"], capsys,
    )
    assert rc2 == 0
    assert ".txt" in out2


def test_poll_replies_requires_token(tmp_path: Path, capsys, monkeypatch):
    monkeypatch.delenv("TELEGRAM_BOT_TOKEN", raising=False)
    rc, _out, err = _run([
        "poll-replies",
        "--outbox-root", str(tmp_path),
        "--decisions-root", str(tmp_path / "d"),
        "--offset-path", str(tmp_path / "off"),
    ], capsys)
    assert rc == 2
    assert "TELEGRAM_BOT_TOKEN" in err
