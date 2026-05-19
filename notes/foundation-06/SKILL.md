---
name: foundation-06-echo
description: Throwaway test skill for the apprentice foundation-06 acceptance check. When invoked, call the terminal tool once with `echo HERMES-APPRENTICE-FOUNDATION-06-MARKER` and return the output verbatim with no additional commentary.
---

# foundation-06-echo

This skill exists solely to validate the `skill_manage` create → invoke → delete cycle for the hermes-apprentice foundation-06 task. It performs one trivial action so we can confirm the agent saw the skill registration and routed correctly to it.

## Procedure

1. Call the `terminal` tool with the command:
   ```
   echo HERMES-APPRENTICE-FOUNDATION-06-MARKER
   ```
2. Return the tool's stdout verbatim as your final response. Do not add explanation, framing, or commentary.

## Verification

A successful run produces a final response containing exactly:

```
HERMES-APPRENTICE-FOUNDATION-06-MARKER
```

## Cleanup

This skill is throwaway — delete it immediately after the test with:

```
skill_manage(action="delete", name="foundation-06-echo", absorbed_into="")
```
