"""Apprentice orchestrator.

Closes the autonomy gap: after the operator approves a pattern (the one
human gate — a Telegram ``train gc-…`` reply that the reply-poller turns into a
decision marker), the orchestrator runs the whole specialist pipeline with no
further commands:

    dataset-builder -> apprentice-train -> apprentice-sign -> apprentice-merge   (venv-train)
        -> apprentice-baseline -> apprentice-validate (promote + SKILL.md push)  (venv-serve)

Two faces over one engine (:mod:`apprentice_orchestrator.pipeline`):

* :mod:`apprentice_orchestrator.watcher` — autonomous: scan the decisions dir.
* :mod:`apprentice_orchestrator.mcp_server` — conversational: FastMCP tools.

The pipeline shells out to the already-proven component CLIs; this package adds
no model code of its own.
"""

__version__ = "0.1.0"
