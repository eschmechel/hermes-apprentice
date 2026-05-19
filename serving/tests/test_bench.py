"""Tests for serving-03: latency benchmark. CPU-only."""

from __future__ import annotations

import gzip
import json
import time
from pathlib import Path

from apprentice_serving.bench import (
    compute_stats,
    load_prompts,
    print_stats,
)


def _write_test_dataset(dest: Path, records: list[dict]) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    with gzip.open(dest, "wt", encoding="utf-8") as fp:
        for rec in records:
            fp.write(json.dumps(rec) + "\n")


def test_load_prompts_extracts_last_user_message(tmp_path: Path):
    ds = tmp_path / "test.jsonl.gz"
    _write_test_dataset(ds, [
        {"messages": [
            {"role": "system", "content": "You are helpful."},
            {"role": "user", "content": "What is 2+2?"},
            {"role": "assistant", "content": "4"},
        ]},
        {"messages": [
            {"role": "user", "content": "Hi there."},
            {"role": "assistant", "content": "Hello!"},
            {"role": "user", "content": "Tell me a joke."},
            {"role": "assistant", "content": "Why did the chicken..."},
        ]},
        {"messages": [
            {"role": "user", "content": "Single question."},
        ]},
    ])
    prompts = load_prompts(ds)
    assert prompts == ["What is 2+2?", "Tell me a joke.", "Single question."]


def test_load_prompts_respects_limit(tmp_path: Path):
    ds = tmp_path / "test.jsonl.gz"
    _write_test_dataset(ds, [
        {"messages": [{"role": "user", "content": f"Q{i}"}]}
        for i in range(20)
    ])
    prompts = load_prompts(ds, limit=5)
    assert len(prompts) == 5
    assert prompts == ["Q0", "Q1", "Q2", "Q3", "Q4"]


def test_load_prompts_empty_file(tmp_path: Path):
    ds = tmp_path / "test.jsonl.gz"
    _write_test_dataset(ds, [])
    prompts = load_prompts(ds)
    assert prompts == []


def test_compute_stats_empty():
    stats = compute_stats([])
    assert stats["requests"] == 0
    assert stats["errors"] == 0


def test_compute_stats_all_errors():
    results = [
        {"latency_ms": 100.0, "status_code": 400, "tokens": 0},
        {"latency_ms": 50.0, "status_code": 500, "tokens": 0},
    ]
    stats = compute_stats(results)
    assert stats["errors"] == 2


def test_compute_stats_mixed():
    results = [
        {"latency_ms": 10.0, "status_code": 200, "tokens": 5},
        {"latency_ms": 20.0, "status_code": 200, "tokens": 10},
        {"latency_ms": 30.0, "status_code": 200, "tokens": 15},
        {"latency_ms": 0.0, "status_code": 400, "tokens": 0},
    ]
    stats = compute_stats(results)
    assert stats["requests"] == 4
    assert stats["errors"] == 1
    assert stats["p50_ms"] == 20.0
    assert stats["mean_ms"] == 20.0
    assert stats["min_ms"] == 10.0
    assert stats["max_ms"] == 30.0
    assert stats["tokens_total"] == 30
    assert stats["tokens_mean"] == 10.0


def test_compute_stats_percentiles():
    results = [
        {"latency_ms": float(i), "status_code": 200, "tokens": 1}
        for i in range(1, 101)
    ]
    stats = compute_stats(results)
    assert stats["p50_ms"] == 50.5


def test_run_benchmark_concurrency_actually_parallelizes(monkeypatch):
    """Regression for serving-03 bug: --concurrency > 1 must run in parallel,
    not silently sequentially.  We assert that with concurrency=5 and a
    100ms-per-request fake server, 10 requests complete in well under
    the sequential bound of 1.0s.
    """
    import threading
    from apprentice_serving import bench as bench_mod

    in_flight = 0
    peak_in_flight = 0
    lock = threading.Lock()

    def fake_send_one(_endpoint, _prompt, _max_tokens, _timeout):
        nonlocal in_flight, peak_in_flight
        with lock:
            in_flight += 1
            if in_flight > peak_in_flight:
                peak_in_flight = in_flight
        time.sleep(0.1)
        with lock:
            in_flight -= 1
        return ({}, 200, 5)

    monkeypatch.setattr(bench_mod, "_send_one", fake_send_one)

    prompts = [f"q{i}" for i in range(10)]
    t0 = time.monotonic()
    results = bench_mod.run_benchmark(
        endpoint="http://fake",
        prompts=prompts,
        max_tokens=8,
        concurrency=5,
        warmup=0,
    )
    elapsed = time.monotonic() - t0

    assert len(results) == 10
    assert [r["index"] for r in results] == list(range(10)), "input order preserved"
    assert peak_in_flight >= 2, f"expected concurrent in-flight, peak was {peak_in_flight}"
    assert elapsed < 0.6, f"10x100ms with c=5 should finish well under 1s, got {elapsed:.2f}s"


def test_run_benchmark_sequential_when_concurrency_one(monkeypatch):
    """With concurrency=1 we keep the sequential path (no thread pool overhead)."""
    import threading
    from apprentice_serving import bench as bench_mod

    in_flight = 0
    peak_in_flight = 0
    lock = threading.Lock()

    def fake_send_one(_endpoint, _prompt, _max_tokens, _timeout):
        nonlocal in_flight, peak_in_flight
        with lock:
            in_flight += 1
            if in_flight > peak_in_flight:
                peak_in_flight = in_flight
        time.sleep(0.02)
        with lock:
            in_flight -= 1
        return ({}, 200, 1)

    monkeypatch.setattr(bench_mod, "_send_one", fake_send_one)
    bench_mod.run_benchmark(
        endpoint="http://fake",
        prompts=["a", "b", "c"],
        max_tokens=8,
        concurrency=1,
        warmup=0,
    )
    assert peak_in_flight == 1


def test_print_stats_json_output(capfd):
    stats = {
        "p50_ms": 42.0, "p95_ms": 80.0, "p99_ms": 120.0,
        "mean_ms": 50.0, "min_ms": 10.0, "max_ms": 200.0,
        "requests": 100, "errors": 1, "tokens_total": 5000, "tokens_mean": 50.0,
    }
    print_stats(stats, json_output=True)
    out = capfd.readouterr()[0]
    parsed = json.loads(out)
    assert parsed["p50_ms"] == 42.0
    assert parsed["requests"] == 100
