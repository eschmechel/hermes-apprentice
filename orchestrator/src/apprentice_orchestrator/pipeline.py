"""The pipeline engine: approval -> promoted specialist, no human commands.

``run_pipeline`` chains the already-proven component CLIs as subprocesses, each
in the correct venv (train vs serve), recording :class:`~jobs.JobState` after
every step. GPU steps run sequentially (each CLI is its own process that exits
and frees VRAM — the 8 GB-card reality). A validate-gate failure is reported to
the operator via the Telegram failure-enqueue path; it is not a crash.

No model code lives here — this is orchestration only.
"""

from __future__ import annotations

import logging
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Callable

from datetime import datetime, timezone

from . import jobs, placement
from .config import Config, latest_version_dir
from .jobs import JobState
from .venvs import tool

LOG = logging.getLogger("apprentice_orchestrator.pipeline")

# runner(argv, log_path) -> (returncode, stdout, stderr)
Runner = Callable[[list[str], Path], "tuple[int, str, str]"]


def _default_runner(argv: list[str], log_path: Path) -> tuple[int, str, str]:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    proc = subprocess.run(argv, capture_output=True, text=True)
    log_path.write_text(
        f"$ {' '.join(argv)}\n\n=== stdout ===\n{proc.stdout}\n=== stderr ===\n{proc.stderr}",
        encoding="utf-8",
    )
    return proc.returncode, proc.stdout, proc.stderr


def _telegram_bin() -> str | None:
    """apprentice-telegram is an orchestrator dependency, so it sits next to our
    own interpreter; fall back to PATH."""
    cand = Path(sys.executable).parent / "apprentice-telegram"
    if cand.exists():
        return str(cand)
    return shutil.which("apprentice-telegram")


class PipelineError(Exception):
    """A step exited non-zero. ``gate_failed`` distinguishes a validator gate
    rejection (operator-actionable) from an infrastructure error."""

    def __init__(self, message: str, *, gate_failed: bool = False):
        super().__init__(message)
        self.gate_failed = gate_failed


def run_pipeline(
    pattern_id: str,
    *,
    cfg: Config | None = None,
    dataset_dir: str | Path | None = None,
    job: JobState | None = None,
    runner: Runner | None = None,
) -> JobState:
    """Run dataset→train→sign→merge→baseline→validate→promote for ``pattern_id``.

    Returns the terminal :class:`JobState` (status passed/failed). Never raises
    for an expected failure (gate rejection or step error) — those land in the
    JobState and, for the validate gate, a Telegram failure message.
    """
    cfg = cfg or Config()
    real_run = runner is None  # only touch the real GPU (placement) on real runs
    runner = runner or _default_runner
    job = job or JobState(job_id=jobs.new_job_id(pattern_id), pattern_id=pattern_id)
    logs_root = cfg.jobs_dir / job.job_id
    job.save(cfg.jobs_dir)

    def step(name: str, argv: list[str]) -> str:
        st = job.start_step(name)
        job.save(cfg.jobs_dir)
        rc, out, err = runner(argv, logs_root / f"{name}.log")
        job.finish_step(st, rc, detail=(err or out or "").strip()[-500:] or None)
        job.save(cfg.jobs_dir)
        if rc != 0:
            raise PipelineError(f"{name} exited {rc}", gate_failed=(name == "validate"))
        return out

    try:
        # 1. dataset — reuse an existing versioned dataset if present, else build.
        ds = _resolve_dataset(cfg, pattern_id, dataset_dir, step)
        version = ds.name if ds.name.startswith("v") and ds.name[1:].isdigit() else "v1"
        ckpt = cfg.checkpoints_dir(pattern_id, version)
        merged = cfg.merged_dir(pattern_id, version)
        baseline = cfg.baseline_path(pattern_id, version)
        test_ds = ds / "test.jsonl.gz"
        ckpt.mkdir(parents=True, exist_ok=True)
        merged.mkdir(parents=True, exist_ok=True)
        baseline.parent.mkdir(parents=True, exist_ok=True)

        # 2. train  (venv-train) — free the GPU first per the placement policy
        # (evict the warm serve if it's holding VRAM). Real runs only.
        if real_run:
            gpu = placement.prepare_local_gpu(cfg)
            LOG.info("gpu placement", extra=gpu)
        train_argv = [tool("train", "apprentice-train"),
                      "--dataset-dir", str(ds), "--output-dir", str(ckpt),
                      "--max-steps", str(cfg.max_steps), "-v"]
        if cfg.train_profile:
            train_argv[1:1] = ["--profile", cfg.train_profile]
        step("train", train_argv)

        # 3. sign the training manifest  (venv-train)
        step("sign", [tool("train", "apprentice-sign"), "sign",
                      str(ckpt / "training_manifest.json")])

        # 4. merge LoRA -> fp16, carrying the signed manifest forward  (venv-train)
        step("merge", [tool("train", "apprentice-merge"),
                       "--base-model", cfg.base_model,
                       "--adapter-dir", str(ckpt / "lora-adapter"),
                       "--output-dir", str(merged), "-v"])

        # 5. baseline  (venv-serve)
        step("baseline", [tool("serve", "apprentice-baseline"),
                          "--test-dataset", str(test_ds),
                          "--output", str(baseline),
                          "--baseline-model", cfg.base_model,
                          "--gpu-memory-utilization", str(cfg.gpu_memory_utilization), "-v"])

        # 6. validate -> promote + SKILL.md push  (venv-serve)
        out = step("validate", [tool("serve", "apprentice-validate"),
                                "--pattern-id", pattern_id,
                                "--model-dir", str(merged),
                                "--test-dataset", str(test_ds),
                                "--baseline-pairs", str(baseline),
                                "--gpu-memory-utilization", str(cfg.gpu_memory_utilization), "-v"])
        job.result = _parse_verdict(out)
        job.status = jobs.STATUS_PASSED
        job.current_step = None
        job.save(cfg.jobs_dir)
        LOG.info("pipeline passed; specialist promoted", extra={"pattern_id": pattern_id, "job_id": job.job_id})

    except PipelineError as e:
        job.status = jobs.STATUS_FAILED
        job.error = str(e)
        job.save(cfg.jobs_dir)
        LOG.error("pipeline failed", extra={"pattern_id": pattern_id, "job_id": job.job_id, "error": str(e)})
        if e.gate_failed:
            _notify_failure(cfg, pattern_id, runner, logs_root)
        return job

    # Record cost outside the try/except so a ledger write failure doesn't
    # silently swallow the passed pipeline, and a passed pipeline isn't
    # downgraded to failed by a ledger I/O error.
    _train_step = next((s for s in job.steps if s.name == "train"), None)
    if _train_step and _train_step.started_at and _train_step.ended_at:
        try:
            from . import cost as cost_mod
            cost_mod.record(cfg, pattern_id, job.job_id,
                            _iso_diff_seconds(_train_step.started_at, _train_step.ended_at))
        except Exception:
            LOG.exception("failed to record training cost in ledger (pipeline already passed)")
    return job


def _resolve_dataset(cfg: Config, pattern_id: str, dataset_dir, step) -> Path:
    if dataset_dir:
        return Path(dataset_dir).expanduser()
    existing = latest_version_dir(cfg.datasets_dir(pattern_id))
    if existing is not None:
        return existing
    # Build it. dataset-builder is a Go binary on PATH (host-side).
    db = shutil.which("dataset-builder")
    if not db:
        raise PipelineError("no dataset found and dataset-builder not on PATH")
    argv = [db, "build", "--pattern-id", pattern_id, "--output-dir", str(cfg.root / "datasets")]
    if cfg.observer_url:
        argv += ["--observer-url", cfg.observer_url]
    if cfg.presidio_url:
        argv += ["--presidio-url", cfg.presidio_url]
    step("dataset", argv)
    built = latest_version_dir(cfg.datasets_dir(pattern_id))
    if built is None:
        raise PipelineError("dataset-builder produced no versioned output")
    return built


def _parse_verdict(stdout: str) -> dict | None:
    """The validator prints its verdict JSON to stdout; pull the last JSON object."""
    import json

    start = stdout.find("{")
    while start != -1:
        try:
            return json.loads(stdout[start:])
        except json.JSONDecodeError:
            start = stdout.find("{", start + 1)
    return None


def _iso_diff_seconds(start: str, end: str) -> float:
    fmt = "%Y-%m-%dT%H:%M:%SZ"
    a = datetime.strptime(start, fmt).replace(tzinfo=timezone.utc)
    b = datetime.strptime(end, fmt).replace(tzinfo=timezone.utc)
    return (b - a).total_seconds()


def _notify_failure(cfg: Config, pattern_id: str, runner: Runner, logs_root: Path) -> None:
    """Best-effort: enqueue a Telegram failure message (delivered by the cron)."""
    tg = _telegram_bin()
    if not tg:
        LOG.warning("apprentice-telegram not found; skipping failure notification")
        return
    failures = sorted((cfg.root / "failures" / pattern_id).glob("*/report.md"), reverse=True)
    last_validate = cfg.root / "last-validate.json"
    argv = [tg, "enqueue", "failure", "--outbox-root", str(cfg.outbox_dir)]
    if last_validate.exists():
        argv += ["--validator-result", str(last_validate)]
    if failures:
        argv += ["--failure-report", str(failures[0])]
    rc, _, err = runner(argv, logs_root / "notify-failure.log")
    if rc != 0:
        LOG.warning("failure notification enqueue failed", extra={"error": err[-300:]})
