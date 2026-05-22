"""Tests for apprentice_orchestrator.budget."""

from __future__ import annotations

import json

from apprentice_orchestrator import budget


def test_default_budget(orch_env):
    cfg = orch_env
    b = budget.get_budget(cfg, "acme")
    assert b["tenant_id"] == "acme"
    assert b["budget"] == 20.0
    assert b["spent"] == 0.0
    assert b["remaining"] == 20.0
    assert b["pct"] == 0.0
    assert b["threshold"] is None
    assert b["allowed"] is True


def test_record_spend(orch_env):
    cfg = orch_env
    b = budget.record(cfg, "acme", 5.0, source="training")
    assert b["spent"] == 5.0
    assert b["remaining"] == 15.0
    assert b["pct"] == 25.0


def test_multiple_records_sum(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 5.0)
    budget.record(cfg, "acme", 3.0)
    b = budget.get_budget(cfg, "acme")
    assert b["spent"] == 8.0
    assert b["pct"] == 40.0


def test_threshold_warning(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 16.0)  # 80%
    b = budget.get_budget(cfg, "acme")
    assert b["threshold"] == "warning"


def test_threshold_critical(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 19.0)  # 95%
    b = budget.get_budget(cfg, "acme")
    assert b["threshold"] == "critical"


def test_threshold_exhausted(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 20.0)  # 100%
    b = budget.get_budget(cfg, "acme")
    assert b["threshold"] == "exhausted"
    assert b["allowed"] is False


def test_threshold_over_exhausted(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 25.0)
    b = budget.get_budget(cfg, "acme")
    assert b["threshold"] == "exhausted"
    assert b["allowed"] is False


def test_set_budget(orch_env):
    cfg = orch_env
    b = budget.set_budget(cfg, "acme", 50.0)
    assert b["budget"] == 50.0
    assert b["spent"] == 0.0

    budget.record(cfg, "acme", 10.0)
    b = budget.get_budget(cfg, "acme")
    assert b["pct"] == 20.0


def test_budget_increase(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 18.0)  # 90%
    b = budget.budget_increase(cfg, "acme", 30.0)  # +$30 => $50
    assert b["budget"] == 50.0
    assert b["pct"] == 36.0  # 18/50 = 36%


def test_budget_history(orch_env):
    cfg = orch_env
    budget.record(cfg, "acme", 5.0, source="test-a")
    budget.record(cfg, "acme", 3.0, source="test-b")
    history = budget.budget_history(cfg, "acme", limit=10)
    assert len(history) == 2
    assert history[0]["source"] == "test-b"  # newest first
    assert history[1]["source"] == "test-a"


def test_budget_history_empty(orch_env):
    cfg = orch_env
    assert budget.budget_history(cfg, "nonexistent") == []


def test_cli_budget_get(orch_env, capsys):
    from apprentice_orchestrator import cli
    budget.record(orch_env, "acme", 5.0)
    cli.main(["budget", "get", "--tenant-id", "acme"])
    out = capsys.readouterr().out
    data = json.loads(out)
    assert data["spent"] == 5.0


def test_cli_budget_set(orch_env, capsys):
    from apprentice_orchestrator import cli
    cli.main(["budget", "set", "--tenant-id", "acme", "--monthly-budget-usd", "100"])
    out = capsys.readouterr().out
    data = json.loads(out)
    assert data["budget"] == 100.0


def test_cli_budget_increase(orch_env, capsys):
    from apprentice_orchestrator import cli
    budget.set_budget(orch_env, "acme", 20.0)
    cli.main(["budget", "increase", "--tenant-id", "acme", "--additional-usd", "10"])
    out = capsys.readouterr().out
    data = json.loads(out)
    assert data["budget"] == 30.0


def test_cli_budget_history(orch_env, capsys):
    from apprentice_orchestrator import cli
    budget.record(orch_env, "acme", 1.0)
    budget.record(orch_env, "acme", 2.0)
    cli.main(["budget", "history", "--tenant-id", "acme", "--limit", "5"])
    out = capsys.readouterr().out
    data = json.loads(out)
    assert len(data) == 2
