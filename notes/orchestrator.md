# Orchestrator — closing the autonomy loop

The orchestrator turns a single operator approval into a promoted specialist
with no further commands. It is pure orchestration: it shells out to the proven
component CLIs, each in the correct venv.

```
detector (cron) --approved--> apprentice-orchestrator notify
        -> Telegram graduation  +  ~/.apprentice/candidates/<cid>.json
                         |
   operator taps "train gc-…"   (the ONE human gate)
                         v
   reply-poller writes ~/.apprentice/decisions/<ts>-train-<cid>.json
                         |
   apprentice-orchestrator tick   (host cron)        OR   MCP dispatch_training()
                         v
   dataset-builder build               (host, PATH)
   apprentice-train / sign / merge     (venv-train — Unsloth)
   apprentice-baseline / validate      (venv-serve — vLLM)  -> promote + SKILL.md push
   on gate failure: apprentice-telegram enqueue failure -> outbox -> Telegram
```

## Why two venvs

Unsloth and vLLM pin incompatible torch builds and cannot coexist (see
`notes/` / the caveat run). The pipeline invokes each step with the matching
interpreter, resolved from:

- `APPRENTICE_VENV_TRAIN` (default `~/.apprentice/venv-train`) — train/sign/merge
- `APPRENTICE_VENV_SERVE` (default `~/.apprentice/venv-serve`) — baseline/validate

GPU steps run sequentially (each CLI is its own process that exits and frees
VRAM — required on the 8 GB card).

## cid → pattern

The Telegram cid `gc-<8hex>` is `sha256(pattern_id, salt)` — not reversible. So
`apprentice-orchestrator notify` records `~/.apprentice/candidates/<cid>.json`
at graduation time; the watcher reads it to map a `train gc-…` marker back to a
pattern. (Fallback: brute-force recompute over `~/.apprentice/patterns/*/`.)

## Install + run

```bash
# orchestrator runs in any venv that has apprentice-telegram available
uv pip install -e ./orchestrator -e ./telegram          # + [mcp] extra for the MCP server

# Face 1 — autonomous, from a host cron (mirrors the telegram crons):
*/2 * * * *  apprentice-orchestrator tick

# Manual trigger (reuse an existing dataset, skip the builder):
apprentice-orchestrator run --pattern-id demo-pattern --dataset-dir ~/.apprentice/datasets/demo-pattern/v1

# Inspect a job:
apprentice-orchestrator status --job-id <id>

# Face 2 — conversational MCP server (register with Hermes):
uv pip install -e './orchestrator[mcp]'
apprentice-orchestrator-mcp      # tools: dispatch_training, job_status, list_pattern_candidates
```

Config knobs (env): `APPRENTICE_ROOT`, `APPRENTICE_BASE_MODEL`,
`APPRENTICE_TRAIN_PROFILE`, `APPRENTICE_MAX_STEPS`, `APPRENTICE_GPU_MEM_UTIL`,
`APPRENTICE_OBSERVER_URL`, `APPRENTICE_PRESIDIO_URL`.

## Job state

`~/.apprentice/jobs/<job_id>.json` tracks status + per-step progress (read by
`status` and the MCP `job_status` tool); per-step logs under
`~/.apprentice/jobs/<job_id>/<step>.log`.

## Tests

`uv run --with-editable ./orchestrator --with-editable ./telegram --with pytest --no-project -- python -m pytest orchestrator/tests` —
fakes every CLI, so the suite is GPU- and network-free.

## Out of scope (v0.2 roadmap)

Multimodal specialist zoo (image→VLM, audio→Whisper, TTS) via a modality-aware
router skill + multi-model vLLM; optional auto-approve to remove the human gate.
