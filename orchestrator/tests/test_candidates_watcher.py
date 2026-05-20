import json

import pytest

# cid computation reuses apprentice_telegram; skip cleanly if it isn't installed.
pytest.importorskip("apprentice_telegram")

from apprentice_orchestrator import candidates, jobs, watcher


def test_candidate_roundtrip(orch_env):
    cfg = orch_env
    cid = candidates.write(cfg, "demo-pattern")
    assert cid.startswith("gc-")
    assert candidates.resolve(cfg, cid) == "demo-pattern"


def test_resolve_falls_back_to_pattern_store(orch_env):
    cfg = orch_env
    # no candidate index written; the pattern manifest exists -> brute-force match
    pdir = cfg.patterns_dir / "demo-pattern"
    pdir.mkdir(parents=True)
    (pdir / "manifest.json").write_text(json.dumps({"id": "demo-pattern"}))
    cid = candidates.compute_cid("demo-pattern")
    assert candidates.resolve(cfg, cid) == "demo-pattern"


def test_tick_trains_on_train_marker(orch_env, runner_factory):
    cfg = orch_env
    (cfg.datasets_dir("demo-pattern") / "v1").mkdir(parents=True)  # dataset present -> skip build
    cid = candidates.write(cfg, "demo-pattern")
    cfg.decisions_dir.mkdir(parents=True)
    (cfg.decisions_dir / f"20260520T120000Z-train-{cid}.json").write_text(
        json.dumps({"action": "train", "cid": cid, "chat_id": 1})
    )
    runner = runner_factory({"apprentice-validate": (0, json.dumps({"verdict": {"passed": True}}))})

    summary = watcher.tick(cfg, runner=runner)

    assert summary["trained"] and summary["trained"][0]["pattern_id"] == "demo-pattern"
    assert summary["trained"][0]["status"] == jobs.STATUS_PASSED
    # marker filed under processed/, none left pending
    assert not list(cfg.decisions_dir.glob("*.json"))
    assert list((cfg.decisions_dir / "processed").glob("*.json"))


def test_tick_unresolved_cid_is_recorded_not_fatal(orch_env, runner_factory):
    cfg = orch_env
    cfg.decisions_dir.mkdir(parents=True)
    (cfg.decisions_dir / "20260520T120000Z-train-gc-deadbeef.json").write_text(
        json.dumps({"action": "train", "cid": "gc-deadbeef"})
    )
    summary = watcher.tick(cfg, runner=runner_factory())
    assert summary["errors"] and "unresolved" in summary["errors"][0]["error"]
    assert not summary["trained"]


def test_tick_skip_marks_pattern_rejected(orch_env, runner_factory):
    cfg = orch_env
    pdir = cfg.patterns_dir / "demo-pattern"
    pdir.mkdir(parents=True)
    (pdir / "manifest.json").write_text(json.dumps({"id": "demo-pattern", "status": "approved"}))
    cid = candidates.write(cfg, "demo-pattern")
    cfg.decisions_dir.mkdir(parents=True)
    (cfg.decisions_dir / f"20260520T120000Z-skip-{cid}.json").write_text(
        json.dumps({"action": "skip", "cid": cid})
    )
    watcher.tick(cfg, runner=runner_factory())
    status = json.loads((pdir / "manifest.json").read_text())["status"]
    assert status == "rejected"
