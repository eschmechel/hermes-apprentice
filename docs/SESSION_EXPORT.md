# Apprentice — Session Export

**Date**: May 17–18, 2026
**Status**: Planning complete, ready for build

## Summary

Apprentice is an open-source system that observes a Hermes Agent's traffic, auto-detects recurring task patterns, fine-tunes small LoRA specialists from session history, and routes future matching requests to a cheap local endpoint. Built for the [Hermes Agent Challenge](https://dev.to/challenges/hermes-agent-2026-05-15) on dev.to ($1K prizes, May 15–31).

Repo: `~/Repos/hermes-apprentice`

## Key Decisions Made

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Contest | dev.to Hermes Agent Challenge (not the ended $25K Creative Hackathon) | Only active contest; $1K; Build + Write tracks |
| Base model | Qwen2.5-1.5B-Instruct (Apache 2.0) | Fits 2080 Ti 11GB for both training and inference; validated JSON output quality |
| Training | Unsloth QLoRA 4-bit, rank 16 | 3-15 min for 200-500 pairs on 2080 Ti |
| LoRA deployment | Merge to fp16 (`save_pretrained_merged`) | Avoids Unsloth-vLLM tokenizer drift; hot-swap is v0.2 |
| Router | Go proxy (deterministic) + Hermes skill (ecosystem visibility) | Combo approach: proxy gives reliable routing, skill gives Hermes integration |
| Cron → Go binary | Shell wrapper script | Required for Hermes `no_agent` mode |
| Embedding | BGE-small via ONNX runtime | Local, fast, free; fallback to OpenRouter if needed |
| Training hardware | Cloud-first (RunPod A100 spot ~$1.19/hr), local 2080 Ti for proof | 2080 Ti works for 1.5B but slow; cloud for speed |
| Data source | Hermes SQLite session DB (`~/.hermes/state.db`), pinned to v0.13.0 | Trajectory export as fallback |

## Research Findings

### Hermes Skill System
- Skills are Markdown with YAML frontmatter, live at `~/.hermes/skills/<name>/SKILL.md`
- External process can write to filesystem; Hermes picks up on `/reload-skills` or next session start
- Skill selector is **LLM-judged** — all skill descriptions injected into system prompt, LLM decides which to load
- No embedding-based automatic matching

### Session DB Schema
- `~/.hermes/state.db` (SQLite, WAL mode)
- `sessions` table: id, source, model, system_prompt, token counts, timestamps
- `messages` table: session_id, role, content, tool_calls, timestamp
- FTS5 virtual table for full-text search
- Trajectory export: `hermes sessions export backup.jsonl`

### vLLM LoRA Hot-Swap
- Available since v0.6.1 with `VLLM_ALLOW_RUNTIME_LORA_UPDATING=True`
- Endpoints: `POST /v1/load_lora_adapter`, `POST /v1/unload_lora_adapter`
- `--enable-lora --max-loras 4 --max-lora-rank 64`
- **Known issue**: Unsloth adapters with added tokens cause lm_head shape mismatch. Fix: use `save_pretrained_merged`

### Unsloth Training
- Qwen2.5-1.5B LoRA rank 16: 3-15 min for 200-500 pairs on RTX 4090
- 2080 Ti 11GB: QLoRA 4-bit, batch size 2-4, ~10-30 min
- Output is standard PEFT format; merge for vLLM compatibility

### RouteLLM Numbers (corrected)
- MT-Bench: ~73% cost savings (up to 85% at aggressive thresholds)
- MMLU: ~29% (not 45%)
- GSM8K: ~33% (not 35%)

### Qwen3-1.7B
- Released April 2025, Apache 2.0. Matches Qwen2.5-3B performance.
- Strong alternative for v0.2; Unsloth adapter saving bug (#3428) being resolved.

## Task Structure

12 features, 73 subtasks, all validated.

### Current State
```
.tmp/tasks/
├── foundation/     — 7 subtasks (✗ pending)
├── observer/       — 5 subtasks (✗ pending)
├── detector/       — 6 subtasks (✗ pending)
├── dataset-builder/ — 8 subtasks (✗ pending)
├── trainer/        — 7 subtasks (✗ pending)
├── serving/        — 4 subtasks (✗ pending)
├── validator/      — 7 subtasks (✗ pending)
├── proxy/          — 8 subtasks (✗ pending)
├── skill/          — 5 subtasks (✗ pending)
├── instrumentation/ — 4 subtasks (✗ pending)
├── telegram/       — 6 subtasks (✗ pending)
└── demo/           — 6 subtasks (✗ pending)
```

### Phase Plan
- **Phase 1** (Days 1-3): foundation → observer → detector
- **Phase 2** (Days 4-6): dataset-builder → trainer → serving
- **Phase 3** (Days 7-9): validator → proxy → skill
- **Phase 4** (Days 10-12): instrumentation → telegram
- **Phase 5** (Days 13-16): demo → essay → submission

### Task Management
```bash
bash .opencode/skill/task-management/router.sh status
bash .opencode/skill/task-management/router.sh next
bash .opencode/skill/task-management/router.sh complete <feature> <seq> "summary"
```

## First Task
`foundation-01`: Install Hermes v0.13.0 and verify it works.

## Key Files
- `docs/refined-feasibility-plan.md` — The approved architecture and strategy
- `docs/task-breakdown.md` — Full 73-task structure
- `.tmp/tasks/` — Task JSON files for the CLI tracker
- The task-management skill is loaded at `.opencode/skill/task-management/`
