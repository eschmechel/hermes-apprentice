"""Resolve the train / serve venv interpreters and console scripts.

Unsloth (training) and vLLM (serving/validation) pin incompatible torch builds
and CANNOT share a venv, so the pipeline invokes each step with the right one:

* ``venv-train`` — apprentice-train / apprentice-sign / apprentice-merge
* ``venv-serve`` — apprentice-baseline / apprentice-validate

Override the locations with ``APPRENTICE_VENV_TRAIN`` / ``APPRENTICE_VENV_SERVE``
(point at the venv root, e.g. ``~/.apprentice/venv-train``).
"""

from __future__ import annotations

import os
from pathlib import Path

_DEFAULTS = {
    "train": ("APPRENTICE_VENV_TRAIN", "venv-train"),
    "serve": ("APPRENTICE_VENV_SERVE", "venv-serve"),
}


def venv_root(which: str) -> Path:
    """Return the venv root dir for ``which`` in {"train", "serve"}."""
    try:
        env_var, default_subdir = _DEFAULTS[which]
    except KeyError:
        raise ValueError(f"unknown venv {which!r}; expected 'train' or 'serve'")
    override = os.environ.get(env_var)
    if override:
        return Path(override).expanduser()
    return Path.home() / ".apprentice" / default_subdir


def tool(which: str, name: str) -> str:
    """Absolute path to console script ``name`` inside the ``which`` venv.

    Does not check existence (let the subprocess surface a clear error) so the
    pipeline can run against fakes in tests by pointing the env vars elsewhere.
    """
    return str(venv_root(which) / "bin" / name)
