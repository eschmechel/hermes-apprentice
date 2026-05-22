import json

from apprentice_orchestrator import cost


def _write_proxy_log(cfg, lines: list[dict]):
    """Write proxy log entries to the default log path."""
    cfg.cost_dir.mkdir(parents=True, exist_ok=True)
    log_path = cfg.cost_dir.parent / "proxy" / "proxy.log"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with open(log_path, "w") as fh:
        for obj in lines:
            fh.write(json.dumps(obj) + "\n")


def test_record_creates_ledger_entry(orch_env):
    cfg = orch_env
    entry = cost.record(cfg, "p1", "j1", 3600.0, teacher_tokens=1000)
    assert entry["pattern_id"] == "p1"
    assert entry["job_id"] == "j1"
    assert entry["train_seconds"] == 3600.0
    assert entry["teacher_tokens"] == 1000
    assert entry["train_cost_usd"] == 0.4  # 1 hour * $0.40/hr

    ledger = cfg.cost_dir / "ledger.jsonl"
    assert ledger.exists()


def test_training_cost_sums_correctly(orch_env):
    cfg = orch_env
    cost.record(cfg, "p1", "j1", 3600.0)  # $0.40
    cost.record(cfg, "p1", "j2", 1800.0)  # $0.20
    cost.record(cfg, "p2", "j3", 3600.0)  # $0.40 (other pattern)

    assert cost.training_cost(cfg, "p1") == 0.6
    assert cost.training_cost(cfg, "p2") == 0.4
    assert cost.training_cost(cfg, "nonexistent") == 0.0


def test_cost_saved_parses_specialist_log_lines(orch_env):
    cfg = orch_env
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "upstream",
         "model": "claude-sonnet-4-20250514", "cost_saved_usd": 0.01},
        {"time": "2026-05-20T10:01:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.01},
        {"time": "2026-05-20T10:02:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.02},
        {"time": "2026-05-20T09:59:00Z", "route_decision": "specialist",
         "pattern_id": "p2", "cost_saved_usd": 0.03},
    ])

    total, earliest = cost.cost_saved(cfg, "p1")
    assert total == 0.03
    assert earliest == "2026-05-20T10:01:00Z"

    total2, earliest2 = cost.cost_saved(cfg, "p2")
    assert total2 == 0.03
    assert earliest2 == "2026-05-20T09:59:00Z"


def test_cost_saved_empty_when_no_logs(orch_env):
    cfg = orch_env
    total, earliest = cost.cost_saved(cfg, "nonexistent")
    assert total == 0.0
    assert earliest is None


def test_roi_computes_full_snapshot(orch_env):
    cfg = orch_env
    cost.record(cfg, "p1", "j1", 3600.0)  # $0.40 train
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.15},
        {"time": "2026-05-20T11:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.15},
        {"time": "2026-05-20T12:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.15},  # breaks even here
    ])

    result = cost.roi(cfg, "p1")
    assert result["pattern_id"] == "p1"
    assert result["train_cost"] == 0.4
    assert result["saved"] == 0.45
    assert result["roi"] == 0.05
    assert result["broke_even"] is True
    assert result["runs"] == 1
    assert result["broke_even_at"] == "2026-05-20T12:00:00Z"
    assert result["earliest_saved"] == "2026-05-20T10:00:00Z"


def test_roi_not_broke_even(orch_env):
    cfg = orch_env
    cost.record(cfg, "p1", "j1", 3600.0)  # $0.40 train
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.05},
    ])

    result = cost.roi(cfg, "p1")
    assert result["broke_even"] is False
    assert result["broke_even_at"] is None
    assert result["roi"] == -0.35


def test_all_patterns_roi(orch_env):
    cfg = orch_env
    cost.record(cfg, "p1", "j1", 3600.0)
    cost.record(cfg, "p2", "j2", 1800.0)

    results = cost.all_patterns_roi(cfg)
    assert len(results) == 2
    assert {r["pattern_id"] for r in results} == {"p1", "p2"}


def test_usage_over_time_buckets(orch_env):
    cfg = orch_env
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.01},
        {"time": "2026-05-20T10:30:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.02},
        {"time": "2026-05-20T11:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.03},
        {"time": "2026-05-20T10:00:00Z", "route_decision": "upstream",
         "model": "claude", "cost_saved_usd": 99},  # ignored
    ])

    buckets = cost.usage_over_time(cfg, "p1", bucket="hour")
    assert len(buckets) == 2
    assert buckets[0]["time"] == "2026-05-20T10"
    assert buckets[0]["requests"] == 2
    assert buckets[0]["cost_saved"] == 0.03
    assert buckets[1]["time"] == "2026-05-20T11"
    assert buckets[1]["requests"] == 1
    assert buckets[1]["cost_saved"] == 0.03


def test_usage_over_time_no_pattern_filter(orch_env):
    cfg = orch_env
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.01},
        {"time": "2026-05-20T10:30:00Z", "route_decision": "specialist",
         "pattern_id": "p2", "cost_saved_usd": 0.02},
    ])

    buckets = cost.usage_over_time(cfg, bucket="day")
    assert len(buckets) == 1
    assert buckets[0]["requests"] == 2
    assert buckets[0]["cost_saved"] == 0.03


def test_proxy_latency_stats(orch_env):
    cfg = orch_env
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "latency_ms": 100},
        {"time": "2026-05-20T10:01:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "latency_ms": 200},
        {"time": "2026-05-20T10:02:00Z", "route_decision": "upstream",
         "model": "claude", "latency_ms": 500},
        {"time": "2026-05-20T10:03:00Z", "route_decision": "upstream",
         "model": "claude", "latency_ms": 700},
    ])

    stats = cost.proxy_latency_stats(cfg)
    assert stats["specialist"]["count"] == 2
    assert stats["specialist"]["avg"] == 150.0
    assert stats["upstream"]["count"] == 2
    assert stats["upstream"]["avg"] == 600.0


def test_usage_over_time_week_bucket(orch_env):
    cfg = orch_env
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.01},
        {"time": "2026-05-27T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.02},
    ])

    buckets = cost.usage_over_time(cfg, "p1", bucket="week")
    assert len(buckets) == 2


def test_proxy_latency_stats_empty(orch_env):
    cfg = orch_env
    stats = cost.proxy_latency_stats(cfg)
    assert stats["specialist"]["count"] == 0
    assert stats["upstream"]["count"] == 0


def test_cli_cost_usage_flag(orch_env, capsys):
    cfg = orch_env
    _write_proxy_log(cfg, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "cost_saved_usd": 0.01},
    ])
    from apprentice_orchestrator import cli

    rc = cli.main(["cost", "--usage", "--bucket", "day"])
    out = capsys.readouterr().out
    assert rc == 0
    data = json.loads(out)
    assert len(data) >= 1
    assert data[0]["requests"] >= 1


def test_cli_cost_latency_flag(orch_env, capsys):
    _write_proxy_log(orch_env, [
        {"time": "2026-05-20T10:00:00Z", "route_decision": "specialist",
         "pattern_id": "p1", "latency_ms": 100},
        {"time": "2026-05-20T10:01:00Z", "route_decision": "upstream",
         "model": "claude", "latency_ms": 300},
    ])
    from apprentice_orchestrator import cli

    rc = cli.main(["cost", "--latency"])
    out = capsys.readouterr().out
    assert rc == 0
    data = json.loads(out)
    assert data["specialist"]["count"] == 1
    assert data["upstream"]["count"] == 1
