# foundation-06 proof

End-to-end validation of the `skill_manage` create → invoke → delete cycle against the live Hermes instance running in the Firecracker microVM.

- VM: `firecracker v1.15.1` + iximiuz labs kernel `v6.18.21`
- Hermes: `v0.14.0 (2026.5.16)` from release tag `v2026.5.16`, commit `a91a57fa`
- Model used for the agent turn: `opencode-go / deepseek-v4-flash`
- Date: 2026-05-19

## Step 1 — register via `skill_manage(action="create")`

Executed from the host over SSH, invoking the Hermes venv python directly so the call exercises the real `tools.skill_manager_tool.skill_manage` entry point — same code path the agent uses.

```bash
ssh root@10.0.2.2 '/usr/local/lib/hermes-agent/venv/bin/python -c "
import sys; sys.path.insert(0, \"/usr/local/lib/hermes-agent\")
from tools.skill_manager_tool import skill_manage
content = open(\"/tmp/foundation-06-SKILL.md\").read()
print(skill_manage(action=\"create\", name=\"foundation-06-echo\", content=content))
"'
```

Result (JSON return value from `skill_manage`):

```json
{
  "success": true,
  "message": "Skill 'foundation-06-echo' created.",
  "path": "foundation-06-echo",
  "skill_md": "/root/.hermes/skills/foundation-06-echo/SKILL.md",
  "hint": "..."
}
```

The SKILL.md content registered is committed at `notes/foundation-06/SKILL.md`.

## Step 2 — discovery confirmed

Used the same `agent.skill_utils.get_all_skills_dirs()` walk that `_find_skill` itself uses:

```
FOUND: /root/.hermes/skills/foundation-06-echo/SKILL.md
```

## Step 3 — agent invocation

```bash
ssh root@10.0.2.2 'hermes chat -q "Use the foundation-06-echo skill and follow its procedure exactly." -s foundation-06-echo --yolo'
```

The agent (one chat turn) executed:

1. `skill_view foundation-06-echo` — read the skill content
2. `terminal $ echo HERMES-APPRENTICE-FOUNDATION-06-MARKER` — exact command from the procedure
3. `skill_manage foundation-06-echo` — **deleted** the skill itself, because the SKILL.md's "Cleanup" section instructed it to do so

Final response (verbatim from the agent):

```
HERMES-APPRENTICE-FOUNDATION-06-MARKER
```

Session metadata:

```
Session:        20260519_001616_74732a
Duration:       13s
Messages:       8 (1 user, 6 tool calls)
```

## Step 4 — deletion confirmed

```
ls: cannot access '/root/.hermes/skills/foundation-06-echo': No such file or directory
foundation-06-echo: NOT FOUND (deletion confirmed)
```

The directory is gone and `_find_skill`'s discovery walk no longer turns it up.

## Acceptance criteria

| Criterion | Status |
|---|---|
| Skill registered via `skill_manage` | ✓ Direct `skill_manage(action="create")` call returned success and wrote `/root/.hermes/skills/foundation-06-echo/SKILL.md` |
| Agent invokes it and returns output | ✓ Single agent turn ran `terminal` per the skill's procedure and returned the marker verbatim |
| Skill can be deleted after test | ✓ Agent itself called `skill_manage(action="delete", ...)` per the SKILL.md's Cleanup section; verified the directory is gone |

## Notes

- The agent doing its own cleanup (step 3 part 3) was a side effect of including a "Cleanup" section in the SKILL.md that explicitly told it to delete. We did not need a follow-up Python call to clean up.
- A first attempt at step 3 failed with HTTP 401 because the user's OpenCode Go API key needed re-keying. Re-running the same chat invocation after `hermes setup` re-keyed the provider succeeded immediately — the registered skill carried over (no re-registration needed).
- Cost was 8 messages / 6 tool calls / 13 seconds on `deepseek-v4-flash`. Cheap.
