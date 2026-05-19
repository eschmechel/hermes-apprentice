"""``apprentice-telegram`` — render, enqueue, dispatch, and poll-replies.

Subcommands:

  enqueue graduation --pattern-id ... --record-count ... --description ...
  enqueue failure    --pattern-id ... --validator-result <result.json>
  enqueue weekly     --summary-json  <summary.json>
  enqueue raw        --kind {graduation|failure|weekly} --pattern-id ... [stdin]

  dispatch-one [--outbox-root <path>]
      Pop the oldest pending message, print it to stdout, exit 0 on success.
      Designed for Hermes ``cron create --no-agent --script <s> --deliver telegram``.

  poll-replies --bot-token-env TELEGRAM_BOT_TOKEN
      One poll cycle of getUpdates → decision markers + queued preview replies.

  list [--state pending|processed]
      Inspect the outbox.

All paths default to ``~/.apprentice/outbox`` / ``~/.apprentice/decisions``
/ ``~/.apprentice/telegram/updates_offset``. Override via flags for tests.
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
from pathlib import Path
from typing import Iterable

from . import outbox as outbox_mod
from . import reply_handler
from . import templates

LOG = logging.getLogger("apprentice_telegram")


# ---------------------------------------------------------------------------
# Subparsers
# ---------------------------------------------------------------------------

def _add_outbox_root(p: argparse.ArgumentParser) -> None:
    p.add_argument(
        "--outbox-root", default=None,
        help="Override outbox root (default: ~/.apprentice/outbox).",
    )


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="apprentice-telegram",
        description="Render, enqueue, and dispatch Apprentice Telegram messages.",
    )
    p.add_argument("-v", "--verbose", action="store_true")
    sub = p.add_subparsers(dest="cmd", required=True)

    # --- enqueue ---
    enq = sub.add_parser("enqueue", help="Render a message and add it to the outbox.")
    # Use a distinct dest so the `raw` subparser's --kind flag doesn't clobber
    # the subparser-name selector.
    enq_sub = enq.add_subparsers(dest="enq_kind", required=True)

    grad = enq_sub.add_parser("graduation")
    grad.add_argument("--pattern-id", required=True)
    grad.add_argument("--record-count", type=int, required=True)
    grad.add_argument("--description", required=True)
    grad.add_argument("--example", action="append", default=[],
                      help="Sample user request (repeatable, up to 3 shown).")
    grad.add_argument("--dataset-path", default=None)
    grad.add_argument("--candidate-salt", default="",
                      help="Bump per re-notification to force a fresh cid.")
    _add_outbox_root(grad)

    fail = enq_sub.add_parser("failure")
    fail.add_argument("--pattern-id", default=None,
                      help="Override pattern id (otherwise read from validator-result).")
    fail.add_argument("--validator-result", required=True,
                      help="Path to apprentice-validate's result JSON.")
    fail.add_argument("--failure-report", default=None,
                      help="Optional path to the failure-report markdown.")
    _add_outbox_root(fail)

    week = enq_sub.add_parser("weekly")
    week.add_argument("--summary-json", required=True,
                      help="Path to a proxy ``summary`` JSON file.")
    week.add_argument("--next-candidate-manifest", default=None,
                      help="Optional pattern manifest JSON for the 'next candidate' line.")
    _add_outbox_root(week)

    raw = enq_sub.add_parser("raw", help="Enqueue stdin verbatim as the chosen kind.")
    raw.add_argument("--kind", required=True, choices=("graduation", "failure", "weekly"))
    raw.add_argument("--pattern-id", required=True)
    _add_outbox_root(raw)

    # --- dispatch-one ---
    disp = sub.add_parser(
        "dispatch-one",
        help="Pop the oldest pending message and print it to stdout (cron consumer).",
    )
    _add_outbox_root(disp)

    # --- poll-replies ---
    poll = sub.add_parser(
        "poll-replies",
        help="One getUpdates cycle → decision markers + queued preview replies.",
    )
    poll.add_argument("--bot-token-env", default="TELEGRAM_BOT_TOKEN",
                      help="Env var holding the Telegram bot token (default: TELEGRAM_BOT_TOKEN).")
    poll.add_argument("--decisions-root", default=None,
                      help="Override decisions root (default: ~/.apprentice/decisions).")
    poll.add_argument("--offset-path", default=None,
                      help="Override updates_offset path "
                           "(default: ~/.apprentice/telegram/updates_offset).")
    _add_outbox_root(poll)

    # --- list ---
    ls = sub.add_parser("list", help="Inspect the outbox.")
    ls.add_argument("--state", choices=("pending", "processed"), default="pending")
    _add_outbox_root(ls)

    return p


# ---------------------------------------------------------------------------
# Handlers
# ---------------------------------------------------------------------------

def _setup_logging(verbose: bool) -> None:
    level = logging.DEBUG if verbose else logging.INFO
    fmt = "%(asctime)s %(levelname)s %(name)s %(message)s"
    logging.basicConfig(level=level, format=fmt, stream=sys.stderr)


def _outbox_root(args: argparse.Namespace) -> Path | None:
    val = getattr(args, "outbox_root", None)
    return Path(val).expanduser().resolve() if val else None


def cmd_enqueue_graduation(args: argparse.Namespace) -> int:
    g = templates.Graduation(
        pattern_id=args.pattern_id,
        record_count=args.record_count,
        description=args.description,
        sample_user_requests=list(args.example),
        dataset_path=args.dataset_path,
        candidate_salt=args.candidate_salt,
    )
    body = templates.render_graduation(g)
    path = outbox_mod.enqueue(
        "graduation", g.pattern_id, body, outbox_root=_outbox_root(args),
    )
    print(json.dumps({"enqueued": str(path), "cid": g.cid}, ensure_ascii=False))
    return 0


def cmd_enqueue_failure(args: argparse.Namespace) -> int:
    result_path = Path(args.validator_result).expanduser().resolve()
    with open(result_path, "r", encoding="utf-8") as f:
        result = json.load(f)
    f_dc = templates.failure_from_validator_result(
        result, failure_report_path=args.failure_report,
    )
    if args.pattern_id:
        f_dc.pattern_id = args.pattern_id
    body = templates.render_failure(f_dc)
    path = outbox_mod.enqueue(
        "failure", f_dc.pattern_id, body, outbox_root=_outbox_root(args),
    )
    print(json.dumps({"enqueued": str(path)}, ensure_ascii=False))
    return 0


def cmd_enqueue_weekly(args: argparse.Namespace) -> int:
    summary_path = Path(args.summary_json).expanduser().resolve()
    with open(summary_path, "r", encoding="utf-8") as f:
        summary = json.load(f)
    w = templates.weekly_from_summary_json(summary)
    if args.next_candidate_manifest:
        mp = Path(args.next_candidate_manifest).expanduser().resolve()
        with open(mp, "r", encoding="utf-8") as f:
            manifest = json.load(f)
        w.next_candidate = templates.graduation_from_pattern_manifest(manifest)
    body = templates.render_weekly(w)
    # Use a synthetic pattern id "weekly" so the file name stays stable.
    path = outbox_mod.enqueue(
        "weekly", "weekly", body, outbox_root=_outbox_root(args),
    )
    print(json.dumps({"enqueued": str(path)}, ensure_ascii=False))
    return 0


def cmd_enqueue_raw(args: argparse.Namespace) -> int:
    body = sys.stdin.read()
    if not body.strip():
        print("apprentice-telegram enqueue raw: stdin is empty", file=sys.stderr)
        return 2
    path = outbox_mod.enqueue(
        args.kind, args.pattern_id, body, outbox_root=_outbox_root(args),
    )
    print(json.dumps({"enqueued": str(path)}, ensure_ascii=False))
    return 0


def cmd_dispatch_one(args: argparse.Namespace) -> int:
    claim = outbox_mod.claim_oldest(_outbox_root(args))
    if claim is None:
        # Empty outbox: silent success. Hermes' cron treats empty stdout as
        # "nothing to deliver this tick" (see _parse_wake_gate semantics in
        # notes/cron-tick-implementation.md).
        return 0
    try:
        sys.stdout.write(claim.body)
        sys.stdout.flush()
    except BrokenPipeError:
        outbox_mod.requeue(claim, _outbox_root(args))
        return 1
    outbox_mod.ack(claim, _outbox_root(args))
    return 0


def cmd_poll_replies(args: argparse.Namespace) -> int:
    token = os.environ.get(args.bot_token_env)
    if not token:
        print(
            f"apprentice-telegram poll-replies: env var {args.bot_token_env} is not set",
            file=sys.stderr,
        )
        return 2
    decisions_root = (
        Path(args.decisions_root).expanduser().resolve() if args.decisions_root else None
    )
    offset_path = (
        Path(args.offset_path).expanduser().resolve() if args.offset_path else None
    )
    markers = reply_handler.poll_once(
        bot_token=token,
        decisions_root=decisions_root,
        outbox_root=_outbox_root(args),
        offset_path=offset_path,
    )
    print(json.dumps(
        {"markers_written": [str(m) for m in markers], "count": len(markers)},
        ensure_ascii=False,
    ))
    return 0


def cmd_list(args: argparse.Namespace) -> int:
    if args.state == "pending":
        paths = outbox_mod.list_pending(_outbox_root(args))
    else:
        paths = outbox_mod.list_processed(_outbox_root(args))
    for p in paths:
        print(str(p))
    return 0


_HANDLERS = {
    ("enqueue", "graduation"): cmd_enqueue_graduation,
    ("enqueue", "failure"): cmd_enqueue_failure,
    ("enqueue", "weekly"): cmd_enqueue_weekly,
    ("enqueue", "raw"): cmd_enqueue_raw,
    ("dispatch-one", None): cmd_dispatch_one,
    ("poll-replies", None): cmd_poll_replies,
    ("list", None): cmd_list,
}


def main(argv: Iterable[str] | None = None) -> int:
    args = build_parser().parse_args(list(argv) if argv is not None else None)
    _setup_logging(args.verbose)
    key = (args.cmd, getattr(args, "enq_kind", None))
    handler = _HANDLERS.get(key)
    if handler is None:
        # argparse already enforces choices, but guard anyway.
        print(f"unknown subcommand: {key}", file=sys.stderr)
        return 2
    return handler(args)


if __name__ == "__main__":
    raise SystemExit(main())
