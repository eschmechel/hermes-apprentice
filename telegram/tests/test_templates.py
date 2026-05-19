"""Template rendering — frontmatter-free plain text destined for Telegram."""

from __future__ import annotations

import datetime

import pytest

from apprentice_telegram import templates


def _utc(year: int, month: int, day: int) -> datetime.datetime:
    return datetime.datetime(year, month, day, tzinfo=datetime.timezone.utc)


def test_correlation_id_is_deterministic_and_short():
    a = templates.correlation_id("pattern-xyz")
    b = templates.correlation_id("pattern-xyz")
    c = templates.correlation_id("pattern-xyz", salt="retry-1")
    assert a == b
    assert a != c
    # gc- prefix + 8 hex chars
    assert a.startswith("gc-")
    assert len(a) == 3 + 8


def test_render_graduation_contains_action_prompts_and_examples():
    g = templates.Graduation(
        pattern_id="abc-123",
        record_count=42,
        description="Extract SKU + quantity from order-confirmation emails.",
        sample_user_requests=[
            "Order #4421 — please confirm SKU AX-7 quantity 3.",
            "What was the SKU on order 9912?",
            "Pull the qty for SKU MX-2 from yesterday.",
            "This fourth one shouldn't render.",
        ],
    )
    out = templates.render_graduation(g)
    assert out.startswith(f"[{g.cid}] ")
    assert "42 similar requests" in out
    assert "Extract SKU" in out
    # First three examples present, fourth NOT.
    assert "Order #4421" in out
    assert "shouldn't render" not in out
    # All three reply verbs are present with the cid baked in.
    for verb in ("train", "details", "skip"):
        assert f"{verb} {g.cid}" in out
    # No trailing extra newlines.
    assert out.endswith("\n")
    assert not out.endswith("\n\n")


def test_render_graduation_truncates_long_examples():
    long_line = "x" * 200
    g = templates.Graduation(
        pattern_id="p", record_count=1, description="d",
        sample_user_requests=[long_line],
    )
    out = templates.render_graduation(g)
    # The original 200-char line should not appear verbatim — it's truncated.
    assert long_line not in out
    # The truncation marker is present.
    assert "…" in out


def test_render_failure_reports_scores_and_reason():
    f = templates.Failure(
        pattern_id="abc-123",
        specialist_em=72.5, specialist_f1=80.1,
        baseline_em=68.0, baseline_f1=75.2,
        teacher_score=85.0,
        reason="specialist did not clear baseline + 10pp margin",
        failure_report_path="/path/to/report.md",
    )
    out = templates.render_failure(f)
    assert "abc-123" in out
    assert "won't deploy" in out.lower()
    assert "72.5%" in out
    assert "80.1%" in out
    assert "68.0%" in out
    assert "85.0%" in out  # teacher
    assert "did not clear" in out
    assert "/path/to/report.md" in out


def test_render_failure_optional_teacher_omitted_cleanly():
    f = templates.Failure(
        pattern_id="p", specialist_em=10, specialist_f1=15,
        baseline_em=20, baseline_f1=25, teacher_score=None,
        reason="r",
    )
    out = templates.render_failure(f)
    assert "teacher" not in out.lower()


def test_render_weekly_basic_numbers_and_top_patterns():
    next_cand = templates.Graduation(
        pattern_id="next-pat", record_count=120,
        description="Summarise bug reports.",
    )
    w = templates.WeeklySummary(
        window_start=_utc(2026, 5, 12),
        window_end=_utc(2026, 5, 19),
        total_requests=1000,
        served_locally=800,
        escalated_to_upstream=200,
        total_cost_usd=4.20,
        total_cost_saved_usd=18.50,
        seconds_saved=327.0,
        next_candidate=next_cand,
        per_pattern=[
            templates.WeeklyPattern("a", 500, 120.0, 9.00),
            templates.WeeklyPattern("b", 200, 90.0, 4.50),
            templates.WeeklyPattern("c", 100, 110.0, 2.00),
        ],
    )
    out = templates.render_weekly(w)
    assert "2026-05-12 → 2026-05-19" in out
    assert "1000" in out
    assert "800" in out
    assert "200" in out
    assert "$4.20" in out
    assert "$18.50" in out
    assert "~327s" in out
    # Top patterns ordered by volume desc.
    a_pos = out.index("a:")
    b_pos = out.index("b:")
    c_pos = out.index("c:")
    assert a_pos < b_pos < c_pos
    # Next-candidate line uses the candidate's cid.
    assert next_cand.cid in out
    assert "train " + next_cand.cid in out


def test_render_weekly_sub_dollar_money_format():
    w = templates.WeeklySummary(
        window_start=_utc(2026, 5, 12),
        window_end=_utc(2026, 5, 19),
        total_requests=10, served_locally=10, escalated_to_upstream=0,
        total_cost_usd=0.0,
        total_cost_saved_usd=0.0053,
        seconds_saved=0.0,
    )
    out = templates.render_weekly(w)
    # 0.0053 → "0.5¢"
    assert "0.5¢" in out


def test_failure_from_validator_result_scales_to_percent():
    result = {
        "pattern_id": "p",
        "specialist_scores": {"exact_match": 0.92, "f1": 0.95},
        "baseline_scores": {"exact_match": 0.55, "f1": 0.60},
        "verdict": {"reason": "baseline did not beat margin",
                    "teacher_score": 88.0},
    }
    f = templates.failure_from_validator_result(result, failure_report_path="r.md")
    assert f.specialist_em == pytest.approx(92.0)
    assert f.specialist_f1 == pytest.approx(95.0)
    assert f.baseline_em == pytest.approx(55.0)
    assert f.baseline_f1 == pytest.approx(60.0)
    assert f.teacher_score == pytest.approx(88.0)
    assert "baseline did not beat" in f.reason
    assert f.failure_report_path == "r.md"


def test_graduation_from_pattern_manifest_accepts_both_key_casings():
    g1 = templates.graduation_from_pattern_manifest({
        "id": "p1", "description": "lowercase keys", "record_count": 50,
    })
    g2 = templates.graduation_from_pattern_manifest({
        "id": "p2", "Description": "TitleCase keys", "RecordCount": 75,
    })
    assert g1.record_count == 50 and "lowercase" in g1.description
    assert g2.record_count == 75 and "TitleCase" in g2.description


def test_weekly_from_summary_json_round_trip():
    summary = {
        "window_start": "2026-05-12T00:00:00Z",
        "window_end":   "2026-05-19T00:00:00Z",
        "totals": {
            "total_requests": 100, "served_locally": 60,
            "escalated_to_upstream": 40, "total_cost_usd": 1.0,
            "total_cost_saved_usd": 2.0, "seconds_saved": 30.0,
        },
        "per_pattern": [
            {"pattern_id": "x", "volume": 60, "avg_latency_ms": 100.0,
             "cost_saved_usd": 1.5},
        ],
    }
    w = templates.weekly_from_summary_json(summary)
    assert w.total_requests == 100
    assert w.served_locally == 60
    assert w.escalated_to_upstream == 40
    assert w.per_pattern[0].pattern_id == "x"
    assert w.window_start == _utc(2026, 5, 12)
