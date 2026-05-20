import json

from apprentice_orchestrator import jobs, pipeline


def _dataset(cfg, pattern_id="p1"):
    ds = cfg.datasets_dir(pattern_id) / "v1"
    ds.mkdir(parents=True)
    return ds


def test_happy_path_runs_all_steps_in_order(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    verdict = "verdict here: " + json.dumps({"verdict": {"passed": True}, "comparison": {"delta_f1": 0.2}})
    runner = runner_factory({"apprentice-validate": (0, verdict)})

    job = pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)

    assert job.status == jobs.STATUS_PASSED
    assert [s.name for s in job.steps] == ["train", "sign", "merge", "baseline", "validate"]
    assert all(s.status == jobs.STATUS_PASSED for s in job.steps)
    assert job.result["verdict"]["passed"] is True


def test_venv_routing(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    runner = runner_factory({"apprentice-validate": (0, json.dumps({"ok": True}))})
    pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)

    joined = runner.joined()
    # train/sign/merge from venv-train; baseline/validate from venv-serve
    assert any("vtrain/bin/apprentice-train" in j for j in joined)
    assert any("vtrain/bin/apprentice-sign" in j for j in joined)
    assert any("vtrain/bin/apprentice-merge" in j for j in joined)
    assert any("vserve/bin/apprentice-baseline" in j for j in joined)
    assert any("vserve/bin/apprentice-validate" in j for j in joined)
    # gpu util passed to the serve steps
    assert any("--gpu-memory-utilization 0.8" in j for j in joined)


def test_train_failure_stops_early(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    runner = runner_factory({"apprentice-train": (1, "")})
    job = pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)

    assert job.status == jobs.STATUS_FAILED
    assert [s.name for s in job.steps] == ["train"]  # never reached sign/merge/...
    assert job.steps[-1].exit_code == 1


def test_gate_failure_is_not_a_crash_and_notifies(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    runner = runner_factory({"apprentice-validate": (1, "")})
    job = pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)

    assert job.status == jobs.STATUS_FAILED
    assert [s.name for s in job.steps] == ["train", "sign", "merge", "baseline", "validate"]
    assert job.steps[-1].status == jobs.STATUS_FAILED
    # the job record persisted and is loadable
    loaded = jobs.load_job(cfg.jobs_dir, job.job_id)
    assert loaded is not None and loaded.status == jobs.STATUS_FAILED


def test_profile_flag_passed_when_configured(orch_env, runner_factory, monkeypatch):
    cfg = orch_env
    cfg.train_profile = "/some/profile.yaml"
    ds = _dataset(cfg)
    runner = runner_factory({"apprentice-validate": (0, "{}")})
    pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)
    train_call = next(j for j in runner.joined() if "apprentice-train" in j)
    assert "--profile /some/profile.yaml" in train_call
