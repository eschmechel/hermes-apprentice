"""Tests for skill_registrar — per-pattern SKILL.md rendering and staging.

The registrar is called from the validator's promotion path; if rendering
breaks we silently produce SKILL.md files that Hermes will reject at
``skill_manage.create``-style validation. The roundtrip-with-yaml test guards
that contract.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
import yaml

from apprentice_validator import skill_registrar


PATTERN_ID = "abcd1234-ef56-7890-1234-567890abcdef"
DESCRIPTION = "Extract product SKU and quantity from order-confirmation emails."


def test_render_skill_md_has_valid_yaml_frontmatter():
    md = skill_registrar.render_skill_md(PATTERN_ID, DESCRIPTION, record_count=42)
    assert md.startswith("---\n")
    end = md.index("\n---\n", 4)
    front = md[4:end]
    data = yaml.safe_load(front)
    assert isinstance(data, dict)
    assert data["name"] == PATTERN_ID  # already lowercase + hex
    # Description trigger should include the pattern's own words verbatim.
    assert "SKU" in data["description"]
    assert "Apprentice proxy" in data["description"]


def test_render_skill_md_collapses_whitespace_and_strips_newlines():
    desc = "Line one.\n\n    Line two with   extra   spaces.\n"
    md = skill_registrar.render_skill_md(PATTERN_ID, desc)
    front_end = md.index("\n---\n", 4)
    data = yaml.safe_load(md[4:front_end])
    assert "Line one. Line two with extra spaces." in data["description"]
    # Multi-line YAML on a single line shouldn't have stray newlines that
    # would break the front-matter parser.
    assert "\n" not in data["description"]


def test_render_skill_md_body_mentions_proxy_routing():
    md = skill_registrar.render_skill_md(PATTERN_ID, DESCRIPTION)
    body = md.split("\n---\n", 1)[1]
    assert "Apprentice proxy" in body
    assert "do not" in body.lower()  # "do not call a separate tool"
    assert DESCRIPTION in body


def test_render_skill_md_record_count_phrase():
    md_some = skill_registrar.render_skill_md(PATTERN_ID, DESCRIPTION, record_count=200)
    md_none = skill_registrar.render_skill_md(PATTERN_ID, DESCRIPTION, record_count=None)
    assert "200 examples" in md_some
    assert "many examples" in md_none


def test_render_skill_md_rejects_empty_description():
    with pytest.raises(ValueError, match="non-empty"):
        skill_registrar.render_skill_md(PATTERN_ID, "   ")


def test_normalize_pattern_id_accepts_uuid():
    assert skill_registrar._normalize_pattern_id(PATTERN_ID) == PATTERN_ID


def test_normalize_pattern_id_replaces_illegal_chars():
    raw = "Order Confirmation Pattern!"
    normalized = skill_registrar._normalize_pattern_id(raw)
    assert normalized == "order-confirmation-pattern-"
    # And it's accepted by the same regex Hermes uses.
    assert skill_registrar._NAME_RE.match(normalized)


def test_normalize_pattern_id_empties_raise():
    with pytest.raises(ValueError, match="empty"):
        skill_registrar._normalize_pattern_id("!!!---")


def test_load_pattern_description_reads_manifest(tmp_path: Path):
    patterns_root = tmp_path / "patterns"
    (patterns_root / PATTERN_ID).mkdir(parents=True)
    (patterns_root / PATTERN_ID / "manifest.json").write_text(json.dumps({
        "id": PATTERN_ID,
        "description": DESCRIPTION,
        "record_count": 123,
    }))
    desc, count = skill_registrar.load_pattern_description(
        PATTERN_ID, patterns_root=patterns_root,
    )
    assert desc == DESCRIPTION
    assert count == 123


def test_load_pattern_description_missing_returns_none(tmp_path: Path):
    desc, count = skill_registrar.load_pattern_description(
        PATTERN_ID, patterns_root=tmp_path,
    )
    assert desc is None
    assert count is None


def test_load_pattern_description_corrupt_returns_none(tmp_path: Path):
    patterns_root = tmp_path / "patterns"
    (patterns_root / PATTERN_ID).mkdir(parents=True)
    (patterns_root / PATTERN_ID / "manifest.json").write_text("{not json")
    desc, count = skill_registrar.load_pattern_description(
        PATTERN_ID, patterns_root=patterns_root,
    )
    assert desc is None
    assert count is None


def test_stage_skill_writes_atomically(tmp_path: Path):
    md = skill_registrar.render_skill_md(PATTERN_ID, DESCRIPTION)
    path = skill_registrar.stage_skill(PATTERN_ID, md, staging_root=tmp_path)
    assert path == tmp_path / PATTERN_ID / "SKILL.md"
    assert path.read_text(encoding="utf-8") == md
    # No leftover .tmp files in the skill dir.
    leftovers = [p for p in path.parent.iterdir() if p.suffix == ".tmp"]
    assert leftovers == []


def test_register_skill_local_only_skips_guest(tmp_path: Path):
    patterns_root = tmp_path / "patterns"
    (patterns_root / PATTERN_ID).mkdir(parents=True)
    (patterns_root / PATTERN_ID / "manifest.json").write_text(json.dumps({
        "description": DESCRIPTION,
        "record_count": 7,
    }))
    staging_root = tmp_path / "stage"

    # Pass guest_host=None to skip the scp step entirely (and avoid invoking
    # ssh during unit tests).
    result = skill_registrar.register_skill(
        pattern_id=PATTERN_ID,
        patterns_root=patterns_root,
        staging_root=staging_root,
        guest_host=None,
    )
    assert result.pattern_id == PATTERN_ID
    assert result.skill_name == PATTERN_ID
    assert result.description_source == "pattern_manifest"
    assert result.guest_push == {"skipped": True}
    staged = Path(result.staged_path)
    assert staged.exists()
    assert "Apprentice proxy" in staged.read_text(encoding="utf-8")
    # The body should use the record_count from the manifest.
    assert "7 examples" in staged.read_text(encoding="utf-8")


def test_register_skill_falls_back_when_no_description(tmp_path: Path):
    result = skill_registrar.register_skill(
        pattern_id=PATTERN_ID,
        patterns_root=tmp_path / "nope",  # missing → fallback
        staging_root=tmp_path / "stage",
        guest_host=None,
    )
    assert result.description_source == "fallback"
    staged = Path(result.staged_path)
    body = staged.read_text(encoding="utf-8")
    assert PATTERN_ID in body
    assert "Apprentice-trained specialist" in body


def test_register_skill_arg_description_wins(tmp_path: Path):
    patterns_root = tmp_path / "patterns"
    (patterns_root / PATTERN_ID).mkdir(parents=True)
    (patterns_root / PATTERN_ID / "manifest.json").write_text(json.dumps({
        "description": "manifest-supplied description",
    }))
    result = skill_registrar.register_skill(
        pattern_id=PATTERN_ID,
        description="explicit override",
        patterns_root=patterns_root,
        staging_root=tmp_path / "stage",
        guest_host=None,
    )
    assert result.description_source == "argument"
    body = Path(result.staged_path).read_text(encoding="utf-8")
    assert "explicit override" in body
    assert "manifest-supplied description" not in body


def test_register_skill_invokes_guest_push_when_configured(tmp_path: Path):
    captured: dict = {}

    def fake_push(staged, *, pattern_id, guest_host, guest_skills_dir):
        captured["staged"] = staged
        captured["pattern_id"] = pattern_id
        captured["guest_host"] = guest_host
        captured["guest_skills_dir"] = guest_skills_dir
        return skill_registrar.GuestPushResult(
            attempted=True, succeeded=True, error=None, command=["fake"],
        )

    result = skill_registrar.register_skill(
        pattern_id=PATTERN_ID,
        description=DESCRIPTION,
        staging_root=tmp_path / "stage",
        guest_host="root@10.0.2.2",
        guest_skills_dir="/root/.hermes/skills",
        push_to_guest_fn=fake_push,
    )
    assert captured["pattern_id"] == PATTERN_ID
    assert captured["guest_host"] == "root@10.0.2.2"
    assert captured["guest_skills_dir"] == "/root/.hermes/skills"
    assert captured["staged"] == Path(result.staged_path)
    assert result.guest_push["succeeded"] is True
    assert result.guest_push["remote_dir"] == f"/root/.hermes/skills/{PATTERN_ID}"


def test_register_skill_guest_push_failure_is_non_fatal(tmp_path: Path):
    def fake_push(staged, *, pattern_id, guest_host, guest_skills_dir):
        return skill_registrar.GuestPushResult(
            attempted=True, succeeded=False, error="connection refused",
            command=["scp"],
        )

    result = skill_registrar.register_skill(
        pattern_id=PATTERN_ID,
        description=DESCRIPTION,
        staging_root=tmp_path / "stage",
        guest_host="root@10.0.2.2",
        push_to_guest_fn=fake_push,
    )
    assert result.guest_push["succeeded"] is False
    assert result.guest_push["error"] == "connection refused"
    assert Path(result.staged_path).exists()


def test_push_to_guest_constructs_expected_commands(tmp_path: Path, monkeypatch):
    """Verifies the scp/ssh argv shape without actually running ssh."""
    calls: list[list[str]] = []

    class _CompletedProcess:
        returncode = 0
        stdout = ""
        stderr = ""

    def fake_run(cmd, **_kwargs):
        calls.append(list(cmd))
        return _CompletedProcess()

    monkeypatch.setattr(skill_registrar.subprocess, "run", fake_run)
    staged = tmp_path / "SKILL.md"
    staged.write_text("---\nname: x\ndescription: y\n---\nbody\n")

    res = skill_registrar.push_to_guest(
        staged,
        pattern_id=PATTERN_ID,
        guest_host="root@10.0.2.2",
        guest_skills_dir="/root/.hermes/skills",
        ssh_bin="ssh",
        scp_bin="scp",
        timeout=5.0,
    )
    assert res.succeeded is True
    assert len(calls) == 2
    # First call: ssh ... mkdir -p /root/.hermes/skills/<id>
    assert calls[0][0] == "ssh"
    assert calls[0][-2] == "root@10.0.2.2"
    assert calls[0][-1] == f"mkdir -p /root/.hermes/skills/{PATTERN_ID}"
    # Second call: scp <staged> root@10.0.2.2:/root/.hermes/skills/<id>/SKILL.md
    assert calls[1][0] == "scp"
    assert calls[1][-2] == str(staged)
    assert calls[1][-1] == f"root@10.0.2.2:/root/.hermes/skills/{PATTERN_ID}/SKILL.md"
