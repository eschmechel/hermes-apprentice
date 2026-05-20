import pytest

from apprentice_orchestrator.config import Config


class FakeRunner:
    """Stand-in for the subprocess runner. ``results`` maps a command-line
    substring to (returncode, stdout); unmatched calls succeed with empty out."""

    def __init__(self, results=None):
        self.calls: list[list[str]] = []
        self.results = results or {}

    def __call__(self, argv, log_path):
        self.calls.append(list(argv))
        joined = " ".join(argv)
        for key, (rc, out) in self.results.items():
            if key in joined:
                return rc, out, ""
        return 0, "", ""

    def joined(self) -> list[str]:
        return [" ".join(c) for c in self.calls]


@pytest.fixture
def orch_env(tmp_path, monkeypatch):
    """Isolate all Apprentice state + venv paths under tmp_path; return Config."""
    monkeypatch.setenv("APPRENTICE_ROOT", str(tmp_path / "root"))
    monkeypatch.setenv("APPRENTICE_VENV_TRAIN", str(tmp_path / "vtrain"))
    monkeypatch.setenv("APPRENTICE_VENV_SERVE", str(tmp_path / "vserve"))
    monkeypatch.delenv("APPRENTICE_TRAIN_PROFILE", raising=False)
    return Config()


@pytest.fixture
def runner_factory():
    return FakeRunner
