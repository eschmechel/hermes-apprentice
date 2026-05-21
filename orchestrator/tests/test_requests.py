import json

from apprentice_orchestrator import jobs, requests, watcher


def test_enqueue_and_pending_roundtrip(orch_env):
    cfg = orch_env
    requests.enqueue(cfg, "job-1", "pat-a")
    pend = requests.pending(cfg)
    assert len(pend) == 1
    data = json.loads(pend[0].read_text())
    assert data["pattern_id"] == "pat-a" and data["job_id"] == "job-1"


def test_tick_drains_request_queue(orch_env, runner_factory):
    cfg = orch_env
    (cfg.datasets_dir("demo-pattern") / "v1").mkdir(parents=True)  # dataset present -> skip build
    job = jobs.JobState(job_id="demo-pattern-abc123", pattern_id="demo-pattern")
    job.save(cfg.jobs_dir)
    requests.enqueue(cfg, job.job_id, "demo-pattern")

    runner = runner_factory({"apprentice-validate": (0, json.dumps({"verdict": {"passed": True}}))})
    summary = watcher.tick(cfg, runner=runner)

    assert summary["trained"] and summary["trained"][0]["pattern_id"] == "demo-pattern"
    assert summary["trained"][0]["job_id"] == "demo-pattern-abc123"  # reused the queued job
    assert summary["trained"][0]["status"] == jobs.STATUS_PASSED
    # request drained -> processed/, none pending
    assert not requests.pending(cfg)
    assert list((cfg.job_requests_dir / "processed").glob("*.json"))


def test_request_missing_pattern_id_is_recorded(orch_env, runner_factory):
    cfg = orch_env
    cfg.job_requests_dir.mkdir(parents=True)
    (cfg.job_requests_dir / "bad.json").write_text(json.dumps({"job_id": "x"}))
    summary = watcher.tick(cfg, runner=runner_factory())
    assert summary["errors"] and "missing pattern_id" in summary["errors"][0]["error"]
    assert not summary["trained"]


def test_one_gpu_budget_caps_jobs(orch_env, runner_factory):
    cfg = orch_env
    (cfg.datasets_dir("p") / "v1").mkdir(parents=True)
    requests.enqueue(cfg, "j1", "p")
    requests.enqueue(cfg, "j2", "p")
    runner = runner_factory({"apprentice-validate": (0, "{}")})
    summary = watcher.tick(cfg, runner=runner, max_jobs=1)
    assert len(summary["trained"]) == 1          # only one ran this tick
    assert len(requests.pending(cfg)) == 1       # the other left for next tick
