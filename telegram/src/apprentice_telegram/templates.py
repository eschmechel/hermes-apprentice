"""Telegram message templates: graduation, failure, weekly summary.

Each render function takes a small dataclass and returns a plain-text message
(Hermes' Telegram adapter uses ``sendMessage`` with default parse mode, so we
avoid Markdown/HTML to dodge entity-escaping bugs).

A short correlation id ``[gc-abcd1234]`` is included at the top of every
graduation message so user replies can target a specific candidate. The id
is derived from a sha256 of (pattern_id, timestamp) — short enough to type,
long enough to disambiguate the small handful of in-flight candidates.

Phrasing is deliberately direct: numbers first, no marketing-speak, no
filler. Sentences read like log lines a human is glancing at while busy.
"""

from __future__ import annotations

import datetime
import hashlib
from dataclasses import dataclass, field
from typing import Iterable

# Short correlation id used for reply matching. 8 hex chars = 32 bits, plenty
# for the few-candidates-at-a-time regime.
_CORRELATION_LEN = 8
_CORRELATION_PREFIX = "gc-"


def correlation_id(pattern_id: str, *, salt: str = "") -> str:
    """Deterministic short id for a pattern at a given moment.

    Same (pattern_id, salt) pair always produces the same id — this is how
    the reply handler maps ``train gc-abcd1234`` back to a specific decision
    marker on disk.
    """
    h = hashlib.sha256()
    h.update(pattern_id.encode("utf-8"))
    h.update(b"|")
    h.update(salt.encode("utf-8"))
    return _CORRELATION_PREFIX + h.hexdigest()[:_CORRELATION_LEN]


@dataclass
class Graduation:
    """A new pattern is ready to train."""
    pattern_id: str
    record_count: int
    description: str
    sample_user_requests: list[str] = field(default_factory=list)
    dataset_path: str | None = None
    candidate_salt: str = ""  # bump per re-notification

    @property
    def cid(self) -> str:
        return correlation_id(self.pattern_id, salt=self.candidate_salt)


@dataclass
class Failure:
    """A training run finished but didn't clear the promotion gate."""
    pattern_id: str
    specialist_em: float       # 0–100 (already percentage-scaled)
    specialist_f1: float
    baseline_em: float
    baseline_f1: float
    teacher_score: float | None
    reason: str                # one short sentence from validator's verdict
    failure_report_path: str | None = None


@dataclass
class WeeklyPattern:
    """Per-pattern row inside a weekly summary."""
    pattern_id: str
    volume: int
    avg_latency_ms: float
    cost_saved_usd: float


@dataclass
class WeeklySummary:
    """The Sunday-night roll-up."""
    window_start: datetime.datetime
    window_end: datetime.datetime
    total_requests: int
    served_locally: int
    escalated_to_upstream: int
    total_cost_usd: float       # what we paid (upstream + infra-marginal)
    total_cost_saved_usd: float # what specialists saved vs all-upstream
    seconds_saved: float        # rough estimate from latency deltas
    next_candidate: Graduation | None = None
    per_pattern: list[WeeklyPattern] = field(default_factory=list)


def _fmt_money(usd: float) -> str:
    if abs(usd) < 0.01:
        return f"${usd*100:.1f}¢"
    if abs(usd) < 1.0:
        return f"${usd:.2f}"
    return f"${usd:,.2f}"


def _fmt_pct(x: float) -> str:
    return f"{x:.1f}%"


def _fmt_window(start: datetime.datetime, end: datetime.datetime) -> str:
    return f"{start.date().isoformat()} → {end.date().isoformat()}"


def render_graduation(g: Graduation) -> str:
    """Graduation candidate notification. User replies decide its fate."""
    lines: list[str] = []
    lines.append(f"[{g.cid}] Graduation candidate ready.")
    lines.append("")
    lines.append(f"I've handled {g.record_count} similar requests fitting:")
    lines.append(f"  {g.description}")
    if g.sample_user_requests:
        lines.append("")
        lines.append("Recent examples:")
        for ex in g.sample_user_requests[:3]:
            # Keep each example one line, ~80 chars max — readable on phone.
            snippet = ex.strip().splitlines()[0]
            if len(snippet) > 80:
                snippet = snippet[:77] + "…"
            lines.append(f"  • {snippet}")
    lines.append("")
    lines.append("Reply with:")
    lines.append(f"  train {g.cid}    — fine-tune a specialist on this pattern")
    lines.append(f"  details {g.cid}  — show the dataset that would be used")
    lines.append(f"  skip {g.cid}     — dismiss; I'll keep escalating these")
    return "\n".join(lines).rstrip() + "\n"


def render_failure(f: Failure) -> str:
    """Honest report: scores in, why the gate refused."""
    lines: list[str] = []
    lines.append(f"Training for {f.pattern_id} failed — won't deploy.")
    lines.append("")
    lines.append("Scores (exact-match / F1):")
    lines.append(f"  specialist : {_fmt_pct(f.specialist_em)} / {_fmt_pct(f.specialist_f1)}")
    lines.append(f"  baseline   : {_fmt_pct(f.baseline_em)} / {_fmt_pct(f.baseline_f1)}")
    if f.teacher_score is not None:
        lines.append(f"  teacher    : {_fmt_pct(f.teacher_score)} (target)")
    lines.append("")
    lines.append(f"Reason: {f.reason}")
    if f.failure_report_path:
        lines.append("")
        lines.append(f"Full report: {f.failure_report_path}")
    return "\n".join(lines).rstrip() + "\n"


def render_weekly(w: WeeklySummary) -> str:
    """The Sunday roll-up. Numbers first, no editorialising."""
    lines: list[str] = []
    lines.append(f"Apprentice weekly — {_fmt_window(w.window_start, w.window_end)}")
    lines.append("")
    lines.append(f"Requests: {w.total_requests}")
    lines.append(f"  served locally : {w.served_locally}")
    lines.append(f"  escalated      : {w.escalated_to_upstream}")
    lines.append("")
    lines.append(f"Cost paid : {_fmt_money(w.total_cost_usd)}")
    lines.append(f"Cost saved: {_fmt_money(w.total_cost_saved_usd)}")
    lines.append(f"Time saved: ~{w.seconds_saved:.0f}s")
    if w.per_pattern:
        lines.append("")
        lines.append("Top patterns:")
        # Sort by volume descending, take 5.
        rows = sorted(w.per_pattern, key=lambda r: r.volume, reverse=True)[:5]
        for r in rows:
            lines.append(
                f"  {r.pattern_id}: {r.volume} reqs, "
                f"{r.avg_latency_ms:.0f}ms avg, "
                f"saved {_fmt_money(r.cost_saved_usd)}"
            )
    if w.next_candidate is not None:
        c = w.next_candidate
        lines.append("")
        lines.append(
            f"Next candidate: {c.description} "
            f"({c.record_count} examples) — reply `train {c.cid}` to start."
        )
    return "\n".join(lines).rstrip() + "\n"


# ---------------------------------------------------------------------------
# JSON adapters — these let host services (validator, instrumentation) build
# the dataclass from their existing structured output without coupling to it.
# ---------------------------------------------------------------------------

def graduation_from_pattern_manifest(
    manifest: dict,
    *,
    sample_user_requests: Iterable[str] | None = None,
    dataset_path: str | None = None,
    candidate_salt: str = "",
) -> Graduation:
    """Build a Graduation from a detector pattern manifest dict."""
    return Graduation(
        pattern_id=manifest["id"],
        record_count=int(manifest.get("record_count") or manifest.get("RecordCount") or 0),
        description=str(manifest.get("description") or manifest.get("Description") or ""),
        sample_user_requests=list(sample_user_requests or []),
        dataset_path=dataset_path,
        candidate_salt=candidate_salt,
    )


def failure_from_validator_result(result: dict, *, failure_report_path: str | None = None) -> Failure:
    """Build a Failure from the validator's result-JSON shape.

    The validator writes ``specialist_scores``, ``baseline_scores``, and
    ``verdict.failure_reason`` (when ``passed=False``) — we surface those.
    """
    pattern_id = str(result.get("pattern_id", "<unknown>"))
    sp = result.get("specialist_scores", {}) or {}
    bl = result.get("baseline_scores", {}) or {}
    verdict = result.get("verdict", {}) or {}
    reason = (
        verdict.get("reason")
        or verdict.get("failure_reason")
        or "specialist did not clear the promotion gate"
    )
    return Failure(
        pattern_id=pattern_id,
        specialist_em=float(sp.get("exact_match", 0.0)) * 100,
        specialist_f1=float(sp.get("f1", 0.0)) * 100,
        baseline_em=float(bl.get("exact_match", 0.0)) * 100,
        baseline_f1=float(bl.get("f1", 0.0)) * 100,
        teacher_score=(
            float(verdict["teacher_score"]) if verdict.get("teacher_score") is not None else None
        ),
        reason=str(reason).strip(),
        failure_report_path=failure_report_path,
    )


def weekly_from_summary_json(summary: dict) -> WeeklySummary:
    """Build a WeeklySummary from the proxy ``summary`` subcommand output."""
    window_start = _parse_dt(summary.get("window_start"))
    window_end = _parse_dt(summary.get("window_end"))
    totals = summary.get("totals", {}) or {}
    per = []
    for row in summary.get("per_pattern", []) or []:
        per.append(WeeklyPattern(
            pattern_id=str(row.get("pattern_id", "")),
            volume=int(row.get("volume", 0)),
            avg_latency_ms=float(row.get("avg_latency_ms", 0.0)),
            cost_saved_usd=float(row.get("cost_saved_usd", 0.0)),
        ))
    return WeeklySummary(
        window_start=window_start,
        window_end=window_end,
        total_requests=int(totals.get("total_requests", sum(r.volume for r in per))),
        served_locally=int(totals.get("served_locally", 0)),
        escalated_to_upstream=int(totals.get("escalated_to_upstream", 0)),
        total_cost_usd=float(totals.get("total_cost_usd", 0.0)),
        total_cost_saved_usd=float(totals.get("total_cost_saved_usd", 0.0)),
        seconds_saved=float(totals.get("seconds_saved", 0.0)),
        per_pattern=per,
    )


def _parse_dt(value) -> datetime.datetime:
    if isinstance(value, datetime.datetime):
        return value
    if isinstance(value, str) and value:
        # Tolerate Z suffix for UTC.
        s = value.replace("Z", "+00:00")
        return datetime.datetime.fromisoformat(s)
    return datetime.datetime.now(datetime.timezone.utc)
