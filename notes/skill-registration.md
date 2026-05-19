# Apprentice → Hermes skill registration (skill-04, skill-05)

When `apprentice-validate` promotes a specialist into the registry, it also
publishes a per-pattern `SKILL.md` so Hermes can match user requests to the
specialist. The skill body tells Hermes-the-agent that **the Apprentice proxy
in front of its chat endpoint already routes matched turns to the specialist
model** — the agent does not need to "call" the specialist itself.

Implemented in `validator/src/apprentice_validator/skill_registrar.py`.

## Path layout

| Where | Path | Who writes | Who reads |
|---|---|---|---|
| Host staging (source of truth) | `~/.apprentice/skills/<pattern-id>/SKILL.md` | `apprentice-validate` after a successful promote | host operator (`scp` retry, demos) |
| Hermes guest | `/root/.hermes/skills/<pattern-id>/SKILL.md` | `apprentice-validate` via `scp root@10.0.2.2:` (best-effort) | Hermes `_find_skill` walks all skills roots at session start |

The host-side staging is authoritative. If the guest push fails (microVM
down, ssh key not present, network blip), `apprentice-validate` logs a
warning, records the error in the JSON result under
`skill_registration.guest_push.error`, and *does not fail* — the promotion
stands and the operator can `scp` the staged file in manually.

## SKILL.md shape

The renderer (`render_skill_md`) emits frontmatter that satisfies Hermes'
`_validate_frontmatter`:

```yaml
---
name: "<pattern-id, normalized to ^[a-z0-9][a-z0-9._-]*$, ≤ 64 chars>"
description: "Specialist for: <pattern description>. Use this skill whenever
              the user's request fits this pattern; the Apprentice proxy will
              route the turn to the matched specialist model."
---
```

The pattern description is sourced in this order:
1. `--pattern-description` on the validator CLI.
2. `description` field of `~/.apprentice/patterns/<pattern-id>/manifest.json`
   (written by the detector's `patternstore.Save`).
3. Generic fallback: *"Apprentice-trained specialist for pattern
   `<id>`. Use when the user's request fits this pattern."*

The body is short and explicit: matched routing is transparent, the
specialist was fine-tuned on `<N>` examples of this pattern, the agent should
respond normally rather than invent a "call the specialist" tool step.

## Reload semantics (skill-05)

Hermes' `clear_skills_system_prompt_cache(clear_snapshot=True)` lives inside
the Hermes Python process and is only triggered when `skill_manage` mutates
a skill from within that process. It is **not exposed over the network**, so
the validator on the host has no way to call it directly.

Instead we rely on **next-session pickup**: per
[`notes/skill_manage-actions.md`](skill_manage-actions.md#caller-surface),
`_find_skill` walks every skills root on demand via
`agent.skill_utils.get_all_skills_dirs()`. Any `SKILL.md` placed in a skills
root before Hermes builds its next system prompt is automatically available.

Practical implication: a new specialist becomes routable inside Hermes at
the start of the next chat session. For cron-driven jobs that means the next
tick; for interactive sessions, the next `hermes chat` invocation.

If a synchronous reload is ever required mid-session, the path is:

```bash
ssh root@10.0.2.2 'pkill -TERM hermes || true' \
  && ssh root@10.0.2.2 'systemctl --user start hermes-agent'
```

(Or simply let the operator launch a new session — that's what the contest
demo does.)

## CLI flags

```
apprentice-validate \
    --pattern-id <id> \
    --model-dir <merged> \
    --test-dataset <test.jsonl.gz> \
    --baseline-pairs <baseline.jsonl>             \
    [--skip-skill-registration]                   \  # stage nothing
    [--skill-staging-root ~/.apprentice/skills]   \  # host staging dir
    [--patterns-root ~/.apprentice/patterns]      \  # description source
    [--pattern-description "..."]                 \  # explicit override
    [--hermes-guest root@10.0.2.2]                \  # empty string skips scp
    [--hermes-guest-skills-dir /root/.hermes/skills]
```

The result JSON gains a `skill_registration` block:

```json
{
  "skill_registration": {
    "pattern_id": "<id>",
    "skill_name": "<normalized id>",
    "staged_path": "~/.apprentice/skills/<id>/SKILL.md",
    "description_source": "pattern_manifest|argument|fallback",
    "guest_push": {
      "skipped": false,
      "attempted": true,
      "succeeded": true,
      "error": null,
      "host": "root@10.0.2.2",
      "remote_dir": "/root/.hermes/skills/<id>"
    }
  }
}
```
