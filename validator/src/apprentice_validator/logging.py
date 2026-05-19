"""Shared JSON logging for the validator package.

Replicates the _JSONFormatter + _setup_logging pattern from
apprentice_trainer.train so the validator emits the same structured JSON
log format without depending on trainer internals beyond manifest_signer.
"""

from __future__ import annotations

import json
import logging
import sys
import time
from typing import Any

LOG = logging.getLogger("apprentice_validator")

_LOG_RECORD_STD_ATTRS = frozenset({
    "args", "msg", "name", "levelname", "levelno", "pathname", "filename",
    "module", "exc_info", "exc_text", "stack_info", "lineno", "funcName",
    "created", "msecs", "relativeCreated", "thread", "threadName",
    "processName", "process", "taskName",
})


class _JSONFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(record.created)),
            "level": record.levelname,
            "msg": record.getMessage(),
            "component": record.name,
        }
        for k, v in record.__dict__.items():
            if k in _LOG_RECORD_STD_ATTRS:
                continue
            payload[k] = v
        return json.dumps(payload, default=str, ensure_ascii=False)


def setup_logging(level: int = logging.INFO) -> None:
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(_JSONFormatter())
    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(level)
