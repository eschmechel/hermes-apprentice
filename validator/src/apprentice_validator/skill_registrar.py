"""Per-pattern Hermes skill registration (skill-04, skill-05).

When the validator promotes a specialist into the registry, this module
renders a ``SKILL.md`` describing the pattern, stages it under
``~/.apprentice/skills/<pattern-id>/SKILL.md``, and (best-effort) copies it
into the Firecracker microVM at ``/root/.hermes/skills/<pattern-id>/SKILL.md``
where Hermes will discover it.

The skill body deliberately tells the agent that routing is transparent:
the Apprentice proxy in front of Hermes' chat endpoint matches the request
against the pattern's centroid and dispatches to the specialist model on a
cosine-similarity match. From the agent's perspective the response simply
arrives — there is no tool to call.

Cross-process reload (skill-05) is handled by *next-session pickup*: Hermes'
``_find_skill`` walks every skills root on demand, so any new ``SKILL.md`` on
disk shows up automatically the next time Hermes builds its system prompt.
We do not attempt an in-process invalidation from the host because
``clear_skills_system_prompt_cache`` lives inside the Hermes process and is
not exposed over the network.
"""

from __future__ import annotations

import json
import logging
import os
import re
import shutil
import subprocess
import tempfile
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any

LOG = logging.getLogger("apprentice_validator.skill")

DEFAULT_STAGING_ROOT = Path.home() / ".apprentice" / "skills"
DEFAULT_PATTERNS_ROOT = Path.home() / ".apprentice" / "patterns"
DEFAULT_GUEST_HOST = "root@10.0.2.2"
DEFAULT_GUEST_SKILLS_DIR = "/root/.hermes/skills"

# Hermes' VALID_NAME_RE — keep in sync with notes/skill_manage-actions.md.
_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9._-]*$")
_MAX_NAME_LEN = 64


def _normalize_pattern_id(pattern_id: str) -> str:
    """Map a raw pattern id (often a UUID) into a Hermes-legal skill name.

    Hermes enforces ``^[a-z0-9][a-z0-9._-]*$`` and ≤ 64 chars. UUIDs already
    pass; arbitrary detector-supplied strings might not.
    """
    s = pattern_id.strip().lower()
    s = re.sub(r"[^a-z0-9._-]+", "-", s)
    s = s.lstrip("-._")
    if not s:
        raise ValueError(f"pattern id {pattern_id!r} reduces to an empty skill name")
    if not _NAME_RE.match(s):
        raise ValueError(f"normalized skill name {s!r} fails Hermes VALID_NAME_RE")
    return s[:_MAX_NAME_LEN]


# Body kept short — the trigger does the matching, the body just tells the
# agent not to invent a "call the specialist" step.
_SKILL_BODY_TEMPLATE = """# {name}

You have matched the **{name}** specialist pattern.

This skill is one of many specialists trained by the Apprentice. Routing is
handled transparently by the Apprentice proxy that sits in front of your chat
endpoint: when the user's request matches this pattern's centroid above the
similarity threshold, the proxy dispatches the turn to the merged specialist
model on the local serving GPU. The model's reply is returned to you as if it
were your own completion.

## What to do

1. Respond to the user's request normally — *do not* call a separate tool to
   "invoke the specialist". The dispatch already happened upstream of you.
2. Trust the response shape. The specialist was fine-tuned on
   {record_count_phrase} examples of this exact pattern, so the format of its
   reply is the format you should produce.
3. If you have additional context the user provided in this turn that the
   specialist could not have seen (e.g. attachments, prior session memory),
   integrate it into your reply.

## Pattern description

> {description}

## Provenance

- Pattern id: `{pattern_id}`
- Registered by: ``apprentice-validate`` on promotion to the registry at
  ``~/.apprentice/registry/{pattern_id}/v<N>/``.
- Signed by the Apprentice trainer's ed25519 key — verify with
  ``apprentice-validate --check-only`` against the registry manifest.
"""


def render_skill_md(
    pattern_id: str,
    description: str,
    *,
    record_count: int | None = None,
) -> str:
    """Render a SKILL.md (frontmatter + body) for a single pattern.

    The returned string is YAML-safe — ``description`` is JSON-encoded inside
    the frontmatter so quotes / colons / newlines don't break the parser.
    """
    name = _normalize_pattern_id(pattern_id)
    if not description or not description.strip():
        raise ValueError("description must be a non-empty string")
    desc_clean = " ".join(description.split())  # collapse whitespace
    # Pre-pend a trigger sentence so semantic matching against the description
    # field (used by Hermes' skill selector) has a strong hit for both the
    # pattern's own words and the canonical "specialist" framing.
    trigger = (
        f"Specialist for: {desc_clean} "
        f"Use this skill whenever the user's request fits this pattern; "
        f"the Apprentice proxy will route the turn to the matched specialist model."
    )
    frontmatter = (
        "---\n"
        f"name: {json.dumps(name, ensure_ascii=False)}\n"
        f"description: {json.dumps(trigger, ensure_ascii=False)}\n"
        "---\n"
    )
    if record_count is None or record_count <= 0:
        record_count_phrase = "many"
    else:
        record_count_phrase = f"{record_count}"
    body = _SKILL_BODY_TEMPLATE.format(
        name=name,
        record_count_phrase=record_count_phrase,
        description=desc_clean,
        pattern_id=pattern_id,
    )
    return frontmatter + "\n" + body


def load_pattern_description(
    pattern_id: str,
    patterns_root: Path | None = None,
) -> tuple[str | None, int | None]:
    """Read the detector-written pattern manifest for ``pattern_id``.

    Returns (description, record_count). Both fields are optional; missing
    files / fields return ``(None, None)`` rather than raising — the caller is
    expected to fall back to an args-supplied description.
    """
    root = Path(patterns_root) if patterns_root else DEFAULT_PATTERNS_ROOT
    manifest_path = root / pattern_id / "manifest.json"
    if not manifest_path.exists():
        return None, None
    try:
        with open(manifest_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        LOG.warning("pattern manifest unreadable", extra={
            "pattern_id": pattern_id,
            "manifest": str(manifest_path),
            "error": str(e),
        })
        return None, None
    desc = data.get("description") or data.get("Description")
    count = data.get("record_count") or data.get("RecordCount")
    if isinstance(count, int) and count > 0:
        return (desc if isinstance(desc, str) and desc.strip() else None), count
    return (desc if isinstance(desc, str) and desc.strip() else None), None


def _atomic_write_text(path: Path, content: str) -> None:
    """Write ``content`` to ``path`` atomically (tempfile in same dir + rename)."""
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=path.name + ".",
        suffix=".tmp",
        dir=path.parent,
    )
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(content)
        os.replace(tmp_name, path)
    except Exception:
        # Best-effort cleanup; replace is the only thing that fails after the
        # write so a leftover .tmp here means we never made it that far.
        try:
            os.unlink(tmp_name)
        except FileNotFoundError:
            pass
        raise


def stage_skill(
    pattern_id: str,
    skill_md: str,
    staging_root: Path | None = None,
) -> Path:
    """Write ``skill_md`` to ``<staging>/<pattern-id>/SKILL.md`` atomically.

    Returns the absolute path of the SKILL.md that was staged.
    """
    root = Path(staging_root) if staging_root else DEFAULT_STAGING_ROOT
    name = _normalize_pattern_id(pattern_id)
    skill_dir = root / name
    skill_path = skill_dir / "SKILL.md"
    _atomic_write_text(skill_path, skill_md)
    return skill_path


@dataclass
class GuestPushResult:
    attempted: bool
    succeeded: bool
    error: str | None
    command: list[str]


def push_to_guest(
    staged_skill_md: Path,
    *,
    pattern_id: str,
    guest_host: str = DEFAULT_GUEST_HOST,
    guest_skills_dir: str = DEFAULT_GUEST_SKILLS_DIR,
    ssh_bin: str | None = None,
    scp_bin: str | None = None,
    timeout: float = 10.0,
) -> GuestPushResult:
    """Best-effort copy of the staged SKILL.md into the Firecracker microVM.

    Uses two subprocess calls: ``ssh ... mkdir -p`` then ``scp``. Failure is
    not fatal — the host-side staging is the source of truth; this push only
    saves the operator from having to sync manually.

    ``ssh_bin`` / ``scp_bin`` exist for tests; pass ``"ssh"`` / ``"scp"`` from
    PATH in production.
    """
    name = _normalize_pattern_id(pattern_id)
    remote_dir = f"{guest_skills_dir.rstrip('/')}/{name}"
    remote_path = f"{guest_host}:{remote_dir}/SKILL.md"

    ssh = ssh_bin or shutil.which("ssh") or "ssh"
    scp = scp_bin or shutil.which("scp") or "scp"

    mkdir_cmd = [
        ssh,
        "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=accept-new",
        "-o", f"ConnectTimeout={int(timeout)}",
        guest_host,
        f"mkdir -p {remote_dir}",
    ]
    scp_cmd = [
        scp,
        "-o", "BatchMode=yes",
        "-o", "StrictHostKeyChecking=accept-new",
        "-o", f"ConnectTimeout={int(timeout)}",
        str(staged_skill_md),
        remote_path,
    ]
    try:
        subprocess.run(
            mkdir_cmd,
            check=True,
            timeout=timeout,
            capture_output=True,
            text=True,
        )
        subprocess.run(
            scp_cmd,
            check=True,
            timeout=timeout,
            capture_output=True,
            text=True,
        )
    except FileNotFoundError as e:
        return GuestPushResult(
            attempted=True, succeeded=False,
            error=f"ssh/scp binary not found: {e}",
            command=scp_cmd,
        )
    except subprocess.TimeoutExpired as e:
        return GuestPushResult(
            attempted=True, succeeded=False,
            error=f"timeout after {timeout}s: {e.cmd}",
            command=scp_cmd,
        )
    except subprocess.CalledProcessError as e:
        return GuestPushResult(
            attempted=True, succeeded=False,
            error=f"{e.cmd[0]} exited {e.returncode}: {(e.stderr or '').strip()}",
            command=scp_cmd,
        )
    return GuestPushResult(
        attempted=True, succeeded=True, error=None, command=scp_cmd,
    )


@dataclass
class RegistrationResult:
    pattern_id: str
    skill_name: str
    staged_path: str
    description_source: str  # "argument" | "pattern_manifest" | "fallback"
    guest_push: dict[str, Any]

    def as_dict(self) -> dict[str, Any]:
        d = asdict(self)
        return d


def register_skill(
    pattern_id: str,
    *,
    description: str | None = None,
    record_count: int | None = None,
    patterns_root: Path | None = None,
    staging_root: Path | None = None,
    guest_host: str | None = DEFAULT_GUEST_HOST,
    guest_skills_dir: str = DEFAULT_GUEST_SKILLS_DIR,
    push_to_guest_fn=push_to_guest,
) -> RegistrationResult:
    """Render → stage → (optionally) push the per-pattern SKILL.md.

    ``description`` precedence:
      1. The ``description`` argument if non-empty.
      2. ``description`` from the pattern manifest at
         ``<patterns_root>/<pattern-id>/manifest.json``.
      3. A generic fallback so registration never blocks promotion.

    ``guest_host`` set to ``None`` or empty string skips the scp step. This is
    useful for tests and for hosts that don't run a Firecracker microVM.
    """
    desc_source = "argument"
    desc = description.strip() if (description and description.strip()) else None
    manifest_count: int | None = None
    if desc is None:
        manifest_desc, manifest_count = load_pattern_description(pattern_id, patterns_root)
        if manifest_desc:
            desc = manifest_desc
            desc_source = "pattern_manifest"
    if desc is None:
        desc = (
            f"Apprentice-trained specialist for pattern {pattern_id}. "
            "Use when the user's request fits this pattern."
        )
        desc_source = "fallback"

    effective_count = record_count if record_count is not None else manifest_count
    skill_md = render_skill_md(pattern_id, desc, record_count=effective_count)
    staged = stage_skill(pattern_id, skill_md, staging_root)

    guest_payload: dict[str, Any] = {"skipped": True}
    if guest_host:
        result = push_to_guest_fn(
            staged,
            pattern_id=pattern_id,
            guest_host=guest_host,
            guest_skills_dir=guest_skills_dir,
        )
        guest_payload = {
            "skipped": False,
            "attempted": result.attempted,
            "succeeded": result.succeeded,
            "error": result.error,
            "host": guest_host,
            "remote_dir": f"{guest_skills_dir.rstrip('/')}/{_normalize_pattern_id(pattern_id)}",
        }
        if result.succeeded:
            LOG.info("skill pushed to hermes guest", extra=guest_payload)
        else:
            LOG.warning("skill guest push failed (staged path is still authoritative)",
                        extra={**guest_payload, "staged_path": str(staged)})

    return RegistrationResult(
        pattern_id=pattern_id,
        skill_name=_normalize_pattern_id(pattern_id),
        staged_path=str(staged),
        description_source=desc_source,
        guest_push=guest_payload,
    )
