# `skill_manage` — Actions and Flow

Source: `tools/skill_manager_tool.py` in Hermes v0.14.0 (release tag `v2026.5.16`, commit `a91a57fa`). Path inside the Firecracker rootfs: `/usr/local/lib/hermes-agent/tools/skill_manager_tool.py`.

Tool registration lives at the bottom of that file: `registry.register(name="skill_manage", toolset="skills", schema=SKILL_MANAGE_SCHEMA, handler=...)`. The OpenAI function-calling schema is `SKILL_MANAGE_SCHEMA` in the same file.

Skills are the agent's procedural memory. New skills land in `~/.hermes/skills/<category?>/<name>/SKILL.md`. Existing skills can also live in external directories listed under `skills.external_dirs`; `_find_skill` searches all of them via `agent.skill_utils.get_all_skills_dirs()`.

## The six actions

| Action | Purpose | Required args | Optional args |
|---|---|---|---|
| `create` | New skill (SKILL.md + dir) | `name`, `content` | `category` |
| `edit` | Full SKILL.md rewrite | `name`, `content` | — |
| `patch` | Find-and-replace inside SKILL.md or a supporting file | `name`, `old_string`, `new_string` | `file_path`, `replace_all` |
| `delete` | Remove a skill entirely | `name` | `absorbed_into` |
| `write_file` | Add/overwrite a supporting file under `references/`, `templates/`, `scripts/`, `assets/` | `name`, `file_path`, `file_content` | — |
| `remove_file` | Remove a supporting file from one of the four allowed subdirs | `name`, `file_path` | — |

`action` and `name` are always required. The tool returns a JSON string (success or `{success: false, error: "..."}`).

## Payload schema

From `SKILL_MANAGE_SCHEMA["parameters"]["properties"]`:

| Param | Type | Used by | Notes |
|---|---|---|---|
| `action` | enum | all | One of: `create`, `patch`, `edit`, `delete`, `write_file`, `remove_file`. |
| `name` | string | all | Validated by `VALID_NAME_RE = ^[a-z0-9][a-z0-9._-]*$`, max 64 chars. |
| `content` | string | `create`, `edit` | Full SKILL.md text (frontmatter `---` block + body). Must parse as YAML, must include `name` and `description`. Body must be non-empty. Limit: `MAX_SKILL_CONTENT_CHARS = 100_000` (~36k tokens). |
| `category` | string | `create` only | Single-segment directory name, validated like `name`. Becomes a parent dir under `~/.hermes/skills/`. |
| `old_string` | string | `patch` | Text to find. Routed through `tools.fuzzy_match.fuzzy_find_and_replace` — tolerates whitespace, indentation, escape-sequence drift. Must match uniquely unless `replace_all=true`. |
| `new_string` | string | `patch` | Replacement. Empty string is allowed (deletion). `None` is rejected. |
| `replace_all` | bool | `patch` | Default `false`. |
| `file_path` | string | `write_file`, `remove_file` (required); `patch` (optional, defaults to `SKILL.md`) | First path segment must be one of `references`, `templates`, `scripts`, `assets`. `..` traversal blocked by `has_traversal_component`. Resolved path must stay within the skill directory (`validate_within_dir`). |
| `file_content` | string | `write_file` | Limit: `MAX_SKILL_FILE_BYTES = 1_048_576` bytes (1 MiB) AND `MAX_SKILL_CONTENT_CHARS = 100_000`. |
| `absorbed_into` | string | `delete` only | `None`/omitted = backward-compat (logs warning). `""` = explicit "no forwarding target". `"<other-skill>"` = consolidation — the target must already exist on disk; rejected if it equals `name`. |

## Flow detail (create / patch / delete)

These three are the lifecycle backbone. Each follows the same shape: **validate args → mutate filesystem atomically → run security scan → roll back on block → update telemetry → invalidate prompt cache**.

### `create` — `_create_skill(name, content, category=None)`

1. `_validate_name(name)` — character set + length.
2. `_validate_category(category)` if provided — single-segment, same character set.
3. `_validate_frontmatter(content)` — must start with `---`, close `---`, parse as YAML mapping, contain `name` and `description`, have a non-empty body.
4. `_validate_content_size(content)` — ≤ 100 000 chars.
5. `_find_skill(name)` — collision check across **all** skills roots (local + external). Rejects if found.
6. `skill_dir = _resolve_skill_dir(name, category)` then `mkdir(parents=True, exist_ok=True)`.
7. `_atomic_write_text(skill_dir / "SKILL.md", content)` — tempfile in the same directory, then `os.replace` via `utils.atomic_replace`.
8. `_security_scan_skill(skill_dir)` — no-op unless `skills.guard_agent_created=true`. If the scan blocks (verdict `allowed=False` or `allowed=None`/"ask"), `shutil.rmtree(skill_dir)` and return the scan report as the error.
9. On success: `agent.prompt_builder.clear_skills_system_prompt_cache(clear_snapshot=True)`.
10. Telemetry: if `tools.skill_provenance.is_background_review()` is true (i.e. this `create` came from the curator's self-improvement fork, not a user-directed call), `tools.skill_usage.mark_agent_created(name)`. Foreground/user-directed `create` calls are NOT marked agent-created — those skills belong to the user and the curator must not touch them.

Return shape on success:
```json
{
  "success": true,
  "message": "Skill 'foo' created.",
  "path": "<relative-to-SKILLS_DIR>",
  "skill_md": "<absolute SKILL.md path>",
  "category": "<if provided>",
  "hint": "To add reference files, ..."
}
```

### `patch` — `_patch_skill(name, old_string, new_string, file_path=None, replace_all=False)`

1. Argument presence checks: `old_string` truthy, `new_string is not None`.
2. `_find_skill(name)` — must exist.
3. Target file resolution:
   - If `file_path` is set: `_validate_file_path(file_path)` then `_resolve_skill_target(skill_dir, file_path)`.
   - Else: target = `skill_dir / "SKILL.md"`.
4. Read target. Run `fuzzy_find_and_replace(content, old_string, new_string, replace_all)`. On no-match, return the error plus a 500-char `file_preview` and a hint formatted by `tools.fuzzy_match.format_no_match_hint` so the agent can self-correct.
5. `_validate_content_size(new_content)` on the result.
6. If patching `SKILL.md` (no `file_path`): re-run `_validate_frontmatter(new_content)` — refuses patches that would break the YAML header.
7. Capture `original_content` for rollback. `_atomic_write_text(target, new_content)`.
8. `_security_scan_skill(skill_dir)` — if blocked, `_atomic_write_text(target, original_content)` to roll back, return error.
9. On success: `clear_skills_system_prompt_cache(clear_snapshot=True)` + `tools.skill_usage.bump_patch(name)`.

### `delete` — `_delete_skill(name, absorbed_into=None)`

1. `_find_skill(name)` — must exist.
2. `_pinned_guard(name)` — reads `tools.skill_usage.get_record(name)`. If `pinned=True`, refuse with a message pointing the user at `hermes curator unpin <name>`. Note: pin guards **only** against deletion; `patch`/`edit`/`write_file` still go through on pinned skills.
3. `absorbed_into` validation (only when non-empty string):
   - Reject if equal to `name`.
   - `_find_skill(absorbed_into)` — the target umbrella must already exist on disk. Caller is expected to create or patch the umbrella *before* deleting.
4. `skills_root = _containing_skills_root(skill_dir)` — figure out which roots dir (local vs external) holds this skill so we don't accidentally `rmdir` the wrong parent.
5. `shutil.rmtree(skill_dir)`.
6. Clean up the category directory if it's now empty AND it's not the skills root itself.
7. On success: `clear_skills_system_prompt_cache(clear_snapshot=True)` + `tools.skill_usage.forget(name)`.

## Cross-cutting behavior

**Atomic writes.** `_atomic_write_text` writes to `.<filename>.tmp.XXXX` in the same directory then `os.replace`s it. The target file is never observable in a partial state.

**Security scan.** `_security_scan_skill` only runs when `skills.guard_agent_created=true` in config (default `false`). The scanner (`tools.skills_guard.scan_skill`) returns `allowed ∈ {True, False, None}`; both `False` and `None` ("ask" — meaning dangerous findings) block agent-created skills. On a block, every action that mutated content rolls back to the pre-action state before returning the error. The defaults-off rationale (lines 60–66): `terminal()` lets the agent run arbitrary code anyway, so the scanner adds friction without meaningful security.

**Prompt cache invalidation.** Any successful mutation calls `agent.prompt_builder.clear_skills_system_prompt_cache(clear_snapshot=True)`. The wrapping `try`/`except` swallows failures — telemetry/cache errors never break the tool.

**Telemetry side effects** (`tools.skill_usage`):
- `create` (only when `is_background_review()`): `mark_agent_created(name)`.
- `patch` / `edit` / `write_file` / `remove_file`: `bump_patch(name)` — bumps the skill's patch count.
- `delete`: `forget(name)` — drops the usage record.

**Return contract.** Every action returns a JSON string. Success carries `{"success": true, "message": ...}` plus action-specific fields. Failure carries `{"success": false, "error": "<human-readable>"}`. The schema-validation errors at the dispatcher level go through `tools.registry.tool_error` (which formats as the same shape).

## Things to know if you're calling this externally

- `name` is the lookup key everywhere. `_find_skill` walks every skills root looking for a directory named `name` containing a `SKILL.md`. Names must be globally unique across all roots — `create` rejects collisions.
- `category` is **only** used at create-time. You cannot move a skill between categories via this tool; you'd have to `create` a new one and `delete` the old.
- `file_path` is interpreted relative to the skill directory. The first segment must be `references`, `templates`, `scripts`, or `assets` — any other prefix is rejected.
- `patch` uses fuzzy matching. Don't pre-normalize whitespace; the matcher handles it. But if you want a guaranteed exact match, include enough surrounding context that `old_string` is unique.
- `delete` with no `absorbed_into` works but logs a warning — downstream consumers (cron jobs referencing the deleted skill, the curator's consolidation classifier) can't tell whether you pruned or consolidated. Always pass `absorbed_into=""` for pure prune, or `absorbed_into="<umbrella>"` for consolidation.

## Caller surface

The toolset is gated by `skills` in `toolsets.py`. Toolsets that include `skill_manage`:
- `skills` (line 41): `["skills_list", "skill_view", "skill_manage"]`
- The same trio is re-exposed in two preset bundles at lines 311 and 335.

Disable the action set by removing `skills` from the agent's enabled toolsets, or by stripping `skill_manage` specifically.
