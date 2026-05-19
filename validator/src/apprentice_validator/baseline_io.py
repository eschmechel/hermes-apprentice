"""Read/write the cached-baseline file consumed by ``apprentice-validate``.

``apprentice-baseline`` runs the base model once per (dataset, base_model) pair
and writes its (expected, actual) pairs here.  ``apprentice-validate`` reads
this file instead of loading the base model itself, so each validator run only
keeps the specialist on the GPU — avoiding the OOM you'd hit loading two
Qwen2.5-1.5B instances back-to-back on a single consumer GPU.

File format: one JSON object per line, each shaped like::

    {"index": int, "expected": str, "actual": str,
     "expected_len": int, "actual_len": int}

Plus a single leading header line::

    {"schema": "apprentice-baseline-pairs", "version": 1,
     "test_dataset": "/abs/path.jsonl.gz", "base_model": "Qwen/...",
     "count": N, "created_at": "2026-..."}

The header lets ``apprentice-validate`` verify the pairs match the test set
it's about to evaluate against, so a stale baseline doesn't silently corrupt
the gate.
"""

from __future__ import annotations

import datetime
import json
import logging
from pathlib import Path
from typing import Any

LOG = logging.getLogger("apprentice_validator.baseline_io")

SCHEMA = "apprentice-baseline-pairs"
SCHEMA_VERSION = 1


def write_pairs(
    *,
    path: Path,
    pairs: list[dict[str, Any]],
    test_dataset: Path,
    base_model: str,
) -> None:
    """Atomically write the baseline pairs file to *path*."""
    path = path.expanduser().resolve()
    path.parent.mkdir(parents=True, exist_ok=True)

    header = {
        "schema": SCHEMA,
        "version": SCHEMA_VERSION,
        "test_dataset": str(test_dataset.expanduser().resolve()),
        "base_model": base_model,
        "count": len(pairs),
        "created_at": datetime.datetime.now(datetime.timezone.utc)
            .isoformat()
            .replace("+00:00", "Z"),
    }

    tmp = path.with_suffix(path.suffix + ".tmp")
    with tmp.open("w", encoding="utf-8") as fp:
        fp.write(json.dumps(header, sort_keys=True, ensure_ascii=False) + "\n")
        for pair in pairs:
            fp.write(json.dumps(pair, sort_keys=True, ensure_ascii=False) + "\n")
    tmp.replace(path)
    LOG.info("baseline pairs written", extra={
        "path": str(path),
        "count": len(pairs),
        "base_model": base_model,
    })


def read_pairs(
    *,
    path: Path,
    expected_test_dataset: Path | None = None,
    expected_count: int | None = None,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Load a baseline pairs file.  Returns (header, pairs).

    If *expected_test_dataset* is given, the file's header must reference the
    same dataset (by resolved absolute path) or a ``ValueError`` is raised.
    If *expected_count* is given, the file must contain exactly that many
    pairs (the test set may have been re-built since baseline was run).
    """
    path = path.expanduser().resolve()
    if not path.exists():
        raise FileNotFoundError(f"baseline pairs file not found: {path}")

    pairs: list[dict[str, Any]] = []
    header: dict[str, Any] | None = None
    with path.open("r", encoding="utf-8") as fp:
        for line_num, line in enumerate(fp, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError as e:
                raise ValueError(f"{path}:{line_num}: invalid JSON: {e}") from e
            if header is None:
                if rec.get("schema") != SCHEMA:
                    raise ValueError(
                        f"{path}: first line is not a {SCHEMA!r} header; "
                        f"got schema={rec.get('schema')!r}"
                    )
                header = rec
                continue
            pairs.append(rec)

    if header is None:
        raise ValueError(f"{path}: file is empty (no header line)")

    if expected_test_dataset is not None:
        want = str(expected_test_dataset.expanduser().resolve())
        got = header.get("test_dataset")
        if got != want:
            raise ValueError(
                f"baseline pairs were run against {got!r}, but validator was "
                f"asked to evaluate {want!r}.  Re-run apprentice-baseline."
            )

    if expected_count is not None and len(pairs) != expected_count:
        raise ValueError(
            f"baseline pairs file has {len(pairs)} records but test set has "
            f"{expected_count}.  Re-run apprentice-baseline against the "
            f"current test set."
        )

    return header, pairs
