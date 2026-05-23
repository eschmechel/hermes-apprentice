"""Cost/ROI ledger: record training cost and compute ROI from proxy logs.

All costs are in USD.  The ledger is a simple append-only JSONL file
(``<root>/cost/ledger.jsonl``).  Proxy log lines are JSON (one per line) written
by the Go proxy in slog format.

Usage:
    from apprentice_orchestrator import cost
    cost.record(cfg, "demo", "demo-abc123", 300.5)
    print(cost.roi(cfg, "demo"))
"""

from __future__ import annotations

import json
import glob as glob_mod
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterator


# ── helpers ────────────────────────────────────────────────────────────────

def _iso_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# slog emits RFC3339 with fractional seconds and a numeric tz offset,
# e.g. "2026-05-21T21:51:31.0123456-07:00" — strict %Y-%m-%dT%H:%M:%SZ
# silently misses it. Accept any of: trailing Z, ±HH:MM, ±HHMM, or none.
_ISO_RE = re.compile(
    r"(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:?\d{2})?$"
)


def _parse_iso(ts: str | None) -> datetime | None:
    if not ts:
        return None
    m = _ISO_RE.match(ts.strip())
    if not m:
        return None
    date, clock, frac, zone = m.groups()
    s = f"{date}T{clock}"
    if frac:
        s += "." + frac[:6]  # datetime.fromisoformat accepts at most microseconds
    if not zone or zone == "Z":
        s += "+00:00"
    elif ":" not in zone:
        s += zone[:3] + ":" + zone[3:]
    else:
        s += zone
    try:
        return datetime.fromisoformat(s).astimezone(timezone.utc)
    except ValueError:
        return None


# ── ledger (write side) ────────────────────────────────────────────────────

def record(cfg, pattern_id: str, job_id: str, train_seconds: float,
           *, teacher_tokens: int = 0) -> dict:
    """Append one training-cost entry to the ledger.

    Returns the entry dict that was written.
    """
    train_cost_usd = train_seconds / 3600.0 * cfg.gpu_hourly_usd
    entry = {
        "ts": _iso_now(),
        "pattern_id": pattern_id,
        "job_id": job_id,
        "train_seconds": round(train_seconds, 1),
        "teacher_tokens": teacher_tokens,
        "gpu_hourly_usd": cfg.gpu_hourly_usd,
        "train_cost_usd": round(train_cost_usd, 6),
    }
    cfg.cost_dir.mkdir(parents=True, exist_ok=True)
    ledger_path = cfg.cost_dir / "ledger.jsonl"
    with open(ledger_path, "a") as fh:
        fh.write(json.dumps(entry, sort_keys=True) + "\n")
        fh.flush()
    return entry


# ── ledger (read side) ─────────────────────────────────────────────────────

def _read_ledger(ledger_path: Path) -> list[dict]:
    if not ledger_path.exists():
        return []
    entries = []
    with open(ledger_path) as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                entries.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return entries


def training_cost(cfg, pattern_id: str) -> float:
    """Sum of ``train_cost_usd`` across all ledger entries for *pattern_id*."""
    ledger_path = cfg.cost_dir / "ledger.jsonl"
    entries = _read_ledger(ledger_path)
    total = 0.0
    for e in entries:
        if e.get("pattern_id") == pattern_id:
            total += float(e.get("train_cost_usd", 0))
    return round(total, 6)


# ── proxy log parsing ──────────────────────────────────────────────────────

def _log_paths(cfg) -> list[Path]:
    """Resolve proxy log glob. Default: ``<root>/proxy/proxy.log``."""
    glob_str = cfg._resolve_proxy_log_glob()
    return [Path(p) for p in glob_mod.glob(glob_str) if Path(p).is_file()]


def _iter_specialist_entries(cfg,
                             pattern_id: str | None = None) -> Iterator[dict]:
    """Yield dicts from the proxy log with route_decision == 'specialist'.

    When *pattern_id* is given, only entries for that pattern are yielded.
    """
    for log_path in _log_paths(cfg):
        try:
            with open(log_path) as fh:
                for line in fh:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        obj = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if obj.get("route_decision") != "specialist":
                        continue
                    if pattern_id is not None and obj.get("pattern_id") != pattern_id:
                        continue
                    yield obj
        except (OSError, IOError):
            continue


def cost_saved(cfg, pattern_id: str) -> tuple[float, str | None]:
    """Parse proxy log lines for *pattern_id*, return (total_saved_usd, earliest_ts).

    Returns ``(0.0, None)`` if no log files or no matching lines.
    """
    total = 0.0
    earliest: str | None = None
    for obj in _iter_specialist_entries(cfg, pattern_id):
        saved = obj.get("cost_saved_usd")
        if isinstance(saved, (int, float)):
            total += float(saved)
        ts = obj.get("time", "")
        if ts and (earliest is None or ts < earliest):
            earliest = ts
    return round(total, 6), earliest


def roi(cfg, pattern_id: str) -> dict:
    """Full ROI snapshot for one pattern — single pass over the proxy log."""
    tc = training_cost(cfg, pattern_id)
    saved = 0.0
    earliest_saved: str | None = None
    cumulative = 0.0
    broke_even_at: str | None = None

    for obj in _iter_specialist_entries(cfg, pattern_id):
        saved_val = obj.get("cost_saved_usd")
        val = float(saved_val) if isinstance(saved_val, (int, float)) else 0.0
        saved += val
        if tc > 0 and broke_even_at is None:
            cumulative += val
            if cumulative >= tc:
                broke_even_at = obj.get("time", "")
        ts = obj.get("time", "")
        if ts and (earliest_saved is None or ts < earliest_saved):
            earliest_saved = ts

    saved = round(saved, 6)
    roi_val = round(saved - tc, 6)
    broke_even = roi_val >= 0 if tc > 0 else True

    # Count ledger runs for this pattern
    ledger_path = cfg.cost_dir / "ledger.jsonl"
    entries = _read_ledger(ledger_path)
    runs = sum(1 for e in entries if e.get("pattern_id") == pattern_id)

    return {
        "pattern_id": pattern_id,
        "train_cost": tc,
        "saved": saved,
        "roi": roi_val,
        "broke_even": broke_even,
        "runs": runs,
        "broke_even_at": broke_even_at,
        "earliest_saved": earliest_saved,
    }


def all_patterns_roi(cfg) -> list[dict]:
    """ROI for every pattern seen in the ledger *or* the proxy log.

    Matches the Go dashboard's no-filter ROI: a pattern that has served
    requests (savings) but has no training-cost ledger entry yet still shows up.
    """
    ledger_path = cfg.cost_dir / "ledger.jsonl"
    entries = _read_ledger(ledger_path)
    patterns = {e["pattern_id"] for e in entries if "pattern_id" in e}
    for obj in _iter_specialist_entries(cfg):
        pid = obj.get("pattern_id")
        if pid:
            patterns.add(pid)
    return [roi(cfg, pid) for pid in sorted(patterns)]


# ── usage over time ────────────────────────────────────────────────────────

_BUCKET_FMTS = {
    "hour": "%Y-%m-%dT%H",
    "day": "%Y-%m-%d",
    "week": "%Y-%W",
}


def usage_over_time(cfg, pattern_id: str | None = None,
                    bucket: str = "day") -> list[dict]:
    """Bucket specialist requests from the proxy log into time windows.

    Returns ``[{time: "2026-05-21", requests: 42, cost_saved: 1.23}, ...]``.
    """
    bucket_fmt = _BUCKET_FMTS.get(bucket, _BUCKET_FMTS["day"])
    buckets: dict[str, dict] = {}

    for obj in _iter_specialist_entries(cfg, pattern_id):
        ts = obj.get("time", "")
        dt = _parse_iso(ts)
        if dt is None:
            continue
        key = dt.strftime(bucket_fmt)
        saved = obj.get("cost_saved_usd")
        saved_val = float(saved) if isinstance(saved, (int, float)) else 0.0
        if key not in buckets:
            buckets[key] = {"time": key, "requests": 0, "cost_saved": 0.0}
        buckets[key]["requests"] += 1
        buckets[key]["cost_saved"] += saved_val

    for b in buckets.values():
        b["cost_saved"] = round(b["cost_saved"], 6)

    return sorted(buckets.values(), key=lambda b: b["time"])


# ── latency stats ──────────────────────────────────────────────────────────

def proxy_latency_stats(cfg) -> dict:
    """Compute avg/p50/p95/p99 latency for specialist vs upstream routes."""
    specialist_lat: list[float] = []
    upstream_lat: list[float] = []

    for log_path in _log_paths(cfg):
        try:
            with open(log_path) as fh:
                for line in fh:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        obj = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    lat = obj.get("latency_ms")
                    if not isinstance(lat, (int, float)):
                        continue
                    route = obj.get("route_decision", "")
                    if route == "specialist":
                        specialist_lat.append(float(lat))
                    elif route in ("upstream", "fallback"):
                        # match the Go dashboard: fallbacks are upstream traffic
                        upstream_lat.append(float(lat))
        except (OSError, IOError):
            continue

    def _stats(vals: list[float]) -> dict:
        if not vals:
            return {"count": 0, "avg": 0.0, "p50": 0.0, "p95": 0.0, "p99": 0.0}
        sorted_vals = sorted(vals)
        n = len(sorted_vals)
        avg = sum(sorted_vals) / n

        def _pct(p: float) -> float:
            idx = int(n * p / 100.0)
            idx = min(idx, n - 1)
            return sorted_vals[idx]

        return {
            "count": n,
            "avg": round(avg, 2),
            "p50": round(_pct(50), 2),
            "p95": round(_pct(95), 2),
            "p99": round(_pct(99), 2),
        }

    return {
        "specialist": _stats(specialist_lat),
        "upstream": _stats(upstream_lat),
    }
