"""Job state — persisted to ``~/.apprentice/jobs/<job_id>.json``.

Both faces (watcher + MCP) read/write the same record so ``job_status`` can
report progress while a run is in flight.
"""

from __future__ import annotations

import json
import os
import tempfile
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path

STATUS_QUEUED = "queued"
STATUS_RUNNING = "running"
STATUS_PASSED = "passed"      # validate gate passed + promoted
STATUS_FAILED = "failed"      # a step errored, or validate gate failed
TERMINAL = {STATUS_PASSED, STATUS_FAILED}


def _now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def new_job_id(pattern_id: str) -> str:
    return f"{pattern_id}-{uuid.uuid4().hex[:8]}"


@dataclass
class StepState:
    name: str
    status: str = STATUS_QUEUED
    started_at: str | None = None
    ended_at: str | None = None
    exit_code: int | None = None
    detail: str | None = None


@dataclass
class JobState:
    job_id: str
    pattern_id: str
    status: str = STATUS_QUEUED
    current_step: str | None = None
    steps: list[StepState] = field(default_factory=list)
    result: dict | None = None          # validator verdict/scores when done
    error: str | None = None
    created_at: str = field(default_factory=_now)
    updated_at: str = field(default_factory=_now)

    # ---- persistence -------------------------------------------------------
    def path(self, jobs_dir: Path) -> Path:
        return jobs_dir / f"{self.job_id}.json"

    def save(self, jobs_dir: Path) -> None:
        jobs_dir.mkdir(parents=True, exist_ok=True)
        self.updated_at = _now()
        payload = asdict(self)
        # atomic write so a concurrent reader never sees a half file
        fd, tmp = tempfile.mkstemp(dir=jobs_dir, suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                json.dump(payload, f, indent=2)
            os.replace(tmp, self.path(jobs_dir))
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)

    # ---- step helpers ------------------------------------------------------
    def start_step(self, name: str) -> StepState:
        step = StepState(name=name, status=STATUS_RUNNING, started_at=_now())
        self.steps.append(step)
        self.status = STATUS_RUNNING
        self.current_step = name
        return step

    def finish_step(self, step: StepState, exit_code: int, detail: str | None = None) -> None:
        step.ended_at = _now()
        step.exit_code = exit_code
        step.status = STATUS_PASSED if exit_code == 0 else STATUS_FAILED
        step.detail = detail


def load_job(jobs_dir: Path, job_id: str) -> JobState | None:
    p = jobs_dir / f"{job_id}.json"
    if not p.exists():
        return None
    data = json.loads(p.read_text(encoding="utf-8"))
    steps = [StepState(**s) for s in data.pop("steps", [])]
    return JobState(steps=steps, **data)
