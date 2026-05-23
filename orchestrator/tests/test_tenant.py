from pathlib import Path

from apprentice_orchestrator.config import Config, namespaced_pattern_id


def test_default_single_tenant_layout_unchanged(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.delenv("APPRENTICE_TENANT_ID", raising=False)
    monkeypatch.delenv("APPRENTICE_TENANT", raising=False)
    cfg = Config()
    assert cfg.tenant_id is None
    assert cfg.base == Path(str(tmp_path))
    assert cfg.jobs_dir == tmp_path / "jobs"
    assert cfg.datasets_dir("p1") == tmp_path / "datasets" / "p1"


def test_tenant_namespaces_state_and_artifacts(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setenv("APPRENTICE_TENANT_ID", "acme")
    cfg = Config()
    base = tmp_path / "tenants" / "acme"
    assert cfg.base == base
    assert cfg.jobs_dir == base / "jobs"
    assert cfg.job_requests_dir == base / "jobs" / "requests"
    assert cfg.decisions_dir == base / "decisions"
    assert cfg.candidates_dir == base / "candidates"
    assert cfg.patterns_dir == base / "patterns"
    assert cfg.cost_dir == base / "cost"
    assert cfg.datasets_dir("p1") == base / "datasets" / "p1"
    assert cfg.merged_dir("p1", "v1") == base / "merged" / "p1" / "v1"


def test_apprentice_tenant_alias(monkeypatch, tmp_path):
    """APPRENTICE_TENANT is accepted as an alias of APPRENTICE_TENANT_ID."""
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.delenv("APPRENTICE_TENANT_ID", raising=False)
    monkeypatch.setenv("APPRENTICE_TENANT", "legacy-name")
    cfg = Config()
    assert cfg.tenant_id == "legacy-name"
    assert cfg.base == tmp_path / "tenants" / "legacy-name"


def test_proxy_log_stays_shared_across_tenants(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setenv("APPRENTICE_TENANT_ID", "acme")
    cfg = Config()
    # The proxy is one shared process; its log is at root, not under the tenant.
    assert cfg._resolve_proxy_log_glob() == str(tmp_path / "proxy" / "proxy.log")


def test_namespaced_pattern_id():
    assert namespaced_pattern_id("acme", "p1") == "acme--p1"
    assert namespaced_pattern_id(None, "p1") == "p1"
    assert namespaced_pattern_id("", "p1") == "p1"


def test_two_tenants_do_not_collide(monkeypatch, tmp_path):
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path))
    monkeypatch.setenv("APPRENTICE_TENANT_ID", "a")
    a = Config()
    monkeypatch.setenv("APPRENTICE_TENANT_ID", "b")
    b = Config()
    assert a.jobs_dir != b.jobs_dir
    assert a.base != b.base
