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


# ── W9: resume + OOM auto-retry ──────────────────────────────────────────────

def test_oom_retry_smaller_footprint(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    # First train (no batch flag) OOMs; the retry (with --batch-size 1) succeeds.
    # Insertion order matters: the retry key is checked before the generic one.
    runner = runner_factory({
        "--batch-size 1": (0, ""),
        "apprentice-train": (1, "RuntimeError: CUDA out of memory. Tried to allocate"),
    })
    job = pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)

    assert job.status == jobs.STATUS_PASSED
    train_steps = [s for s in job.steps if s.name == "train"]
    assert len(train_steps) == 2  # failed attempt + successful retry
    assert train_steps[0].status == jobs.STATUS_FAILED
    assert train_steps[1].status == jobs.STATUS_PASSED
    assert any("--batch-size 1 --max-seq-len 768" in j for j in runner.joined())


def test_oom_retry_not_triggered_for_non_oom_failure(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    runner = runner_factory({"apprentice-train": (1, "some other error")})
    job = pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), runner=runner)
    assert job.status == jobs.STATUS_FAILED
    # only one train attempt, no retry with the smaller footprint
    assert not any("--batch-size 1" in j for j in runner.joined())


def test_resume_skips_already_passed_steps(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    # A prior run got through baseline, then crashed before validate.
    prior = jobs.JobState(job_id="p1-resume01", pattern_id="p1")
    for name in ("train", "sign", "merge", "baseline"):
        st = jobs.StepState(name=name, status=jobs.STATUS_PASSED,
                            started_at="2026-05-21T10:00:00Z", ended_at="2026-05-21T10:05:00Z")
        prior.steps.append(st)
    prior.save(cfg.jobs_dir)

    # This runner FAILS if train/merge/baseline are re-invoked — so the test
    # proves they were skipped. validate succeeds.
    def runner(argv, log_path):
        j = " ".join(argv)
        if any(x in j for x in ("apprentice-train", "apprentice-merge", "apprentice-baseline")):
            return 1, "", "should not have re-run this step"
        return 0, json.dumps({"verdict": {"passed": True}}), ""

    job = pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), job=prior, runner=runner)
    assert job.status == jobs.STATUS_PASSED


def test_resume_false_reruns_everything(orch_env, runner_factory):
    cfg = orch_env
    ds = _dataset(cfg)
    prior = jobs.JobState(job_id="p1-clean01", pattern_id="p1")
    prior.steps.append(jobs.StepState(name="train", status=jobs.STATUS_PASSED,
                                      started_at="2026-05-21T10:00:00Z", ended_at="2026-05-21T10:05:00Z"))
    runner = runner_factory({"apprentice-validate": (0, json.dumps({"ok": True}))})
    pipeline.run_pipeline("p1", cfg=cfg, dataset_dir=str(ds), job=prior, runner=runner, resume=False)
    # train was re-invoked despite the prior passed step
    assert any("apprentice-train" in j for j in runner.joined())
