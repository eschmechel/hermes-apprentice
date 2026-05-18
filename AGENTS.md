# Apprentice — Agent Instructions

This project builds an autonomous specialist-training agent for Hermes.

## Always use conventional commits
- ALL commits MUST follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
- Format: `<type>(<scope>): <description>`
- Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`, `build`, `perf`
- Examples: `feat(foundation): install Hermes v0.13.0 in Firecracker microVM`
- The user runs commands; act as a pair programmer — guide, don't execute autonomously

## Project Docs
- `docs/refined-feasibility-plan.md` — Architecture and strategy
- `docs/task-breakdown.md` — 73-task structure across 12 features
- `docs/SESSION_EXPORT.md` — Full session state, research findings, key decisions

## Task Management
```bash
bash .opencode/skill/task-management/router.sh status
bash .opencode/skill/task-management/router.sh next
bash .opencode/skill/task-management/router.sh complete <feature> <seq> "summary"
```

## Contest
- **Hermes Agent Challenge** on dev.to (May 15–31, $1K prizes)
- Submission format: dev.to article with demo video/link + code link
- Two tracks: Build With Hermes Agent, Write About Hermes Agent

## Key Constraints
- Training hardware: cloud-first (RunPod), local 2080 Ti 11GB for proof only
- Base model: Qwen2.5-1.5B-Instruct (Apache 2.0)
- LoRA deployment: merge to fp16 (avoid tokenizer drift)
- Router: Go proxy (deterministic) + Hermes skill (ecosystem)
- Hermes pinned to v0.13.0
