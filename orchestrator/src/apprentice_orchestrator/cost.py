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
from datetime import datetime, timezone
from pathlib import Path


# ── helpers ────────────────────────────────────────────────────────────────

def _iso_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _parse_iso(ts: str | None) -> datetime | None:
    if not ts:
        return None
    try:
        return datetime.strptime(ts, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
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


def cost_saved(cfg, pattern_id: str) -> tuple[float, str | None]:
    """Parse proxy log lines for *pattern_id*, return (total_saved_usd, earliest_ts).

    Returns ``(0.0, None)`` if no log files or no matching lines.
    """
    total = 0.0
    earliest: str | None = None
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
                    if obj.get("pattern_id") != pattern_id:
                        continue
                    saved = obj.get("cost_saved_usd")
                    if isinstance(saved, (int, float)):
                        total += float(saved)
                    ts = obj.get("time", "")
                    if ts and (earliest is None or ts < earliest):
                        earliest = ts
        except (OSError, IOError):
            continue
    return round(total, 6), earliest


def roi(cfg, pattern_id: str) -> dict:
    """Full ROI snapshot for one pattern."""
    tc = training_cost(cfg, pattern_id)
    saved, earliest_saved = cost_saved(cfg, pattern_id)
    roi_val = round(saved - tc, 6)
    broke_even = roi_val >= 0 if tc > 0 else True

    # Determine break-even time from the proxy log
    broke_even_at: str | None = None
    if tc > 0:
        cumulative = 0.0
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
                        if obj.get("pattern_id") != pattern_id:
                            continue
                        saved_val = obj.get("cost_saved_usd")
                        if isinstance(saved_val, (int, float)):
                            cumulative += float(saved_val)
                        if cumulative >= tc and broke_even_at is None:
                            broke_even_at = obj.get("time", "")
                            break
                if broke_even_at is not None:
                    break
            except (OSError, IOError):
                continue

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
    }


def all_patterns_roi(cfg) -> list[dict]:
    """ROI for every pattern that has a ledger entry."""
    ledger_path = cfg.cost_dir / "ledger.jsonl"
    entries = _read_ledger(ledger_path)
    patterns = list({e["pattern_id"] for e in entries if "pattern_id" in e})
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
                    pid = obj.get("pattern_id", "")
                    if pattern_id is not None and pid != pattern_id:
                        continue
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
        except (OSError, IOError):
            continue

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
                    elif route == "upstream":
                        upstream_lat.append(float(lat))
        except (OSError, IOError):
            continue

    def _pct(vals: list[float], p: float) -> float:
        if not vals:
            return 0.0
        sorted_vals = sorted(vals)
        idx = int(len(sorted_vals) * p / 100.0)
        idx = min(idx, len(sorted_vals) - 1)
        return sorted_vals[idx]

    def _stats(vals: list[float]) -> dict:
        if not vals:
            return {"count": 0, "avg": 0.0, "p50": 0.0, "p95": 0.0, "p99": 0.0}
        return {
            "count": len(vals),
            "avg": round(sum(vals) / len(vals), 2),
            "p50": round(_pct(vals, 50), 2),
            "p95": round(_pct(vals, 95), 2),
            "p99": round(_pct(vals, 99), 2),
        }

    return {
        "specialist": _stats(specialist_lat),
        "upstream": _stats(upstream_lat),
    }
