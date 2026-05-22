# Copilot Instructions for hermes-apprentice

## Project overview

Hermes Apprentice is an autonomous specialist-training agent for the Hermes Agent runtime. It observes conversations, detects patterns, trains small language models (Qwen2.5-1.5B QLoRA), validates them, and deploys them via a Go proxy router. 7 Go packages, 5 Python packages.

## Conventions

- **All commits** follow [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <description>`
- **Scopes:** foundation, observer, detector, dataset-builder, trainer, serving, validator, proxy, skill, instrumentation, telegram, demo, orchestrator
- **Types:** feat, fix, docs, chore, refactor, test, ci, build, perf

## Two venvs

Python packages use two virtual environments because torch conflicts with non-torch packages:

- **venv-train**: trainer, validator, serving (`uv pip install -e trainer -e validator -e serving`)
- **venv-orch**: orchestrator, telegram (`uv pip install -e orchestrator -e telegram`)

## Test commands

```bash
# Go
cd proxy && go test ./... -v

# Python (from each package dir)
pytest tests/ -v
```

Go tests use `httptest.Server` for HTTP mocks, `os.TempDir` for file state.
Python tests use `pytest` with `tmp_path`/`monkeypatch` fixtures, `http.server.HTTPServer` for live server mocks.

## Key architecture rules

- The proxy is the **deterministic router** (BGE-small cosine matching) — no LLM in the routing path
- Hermes skills (SKILL.md) are for **ecosystem visibility only**, not routing
- Baseline and validation run as **separate CLI processes** (file seam) so two LLMs never share GPU
- Outgoing Telegram messages use **Hermes' cron adapter** (`--deliver telegram`), not python-telegram-bot
- Every manifest is **Ed25519-signed** (trainer keygen → sign → validator verify)
- Canary ramp is **self-correcting** (warming→live as agreement scores prove safe, warming→broken on failure)

## File patterns to know

| Pattern | Meaning |
|---------|---------|
| `proxy/internal/canary/` | Traffic ramp state machine |
| `proxy/internal/tenant/` | API key auth |
| `proxy/internal/ratelimit/` | Per-tenant token bucket |
| `proxy/internal/alias/` | Pattern alias resolution (merging) |
| `orchestrator/src/apprentice_orchestrator/watcher.py` | Decision marker tick loop |
| `orchestrator/src/apprentice_orchestrator/mcp_server.py` | MCP tools (training dispatch, budget, quota, merge) |
| `orchestrator/src/apprentice_orchestrator/budget.py` | Monthly monetary budget with Telegram thresholds |
| `orchestrator/src/apprentice_orchestrator/quota.py` | Per-tenant resource quotas |
| `orchestrator/src/apprentice_orchestrator/safety.py` | Canary HTTP API client |
| `deploy/grafana/` | Pre-built dashboards and Prometheus config |
| `deploy/docker/` | Docker Compose files |
