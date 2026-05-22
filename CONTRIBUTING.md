# Contributing

## Philosophy

Apprentice is built for the [Hermes Agent Challenge](https://dev.to/challenges/hermes-agent-2026-05-15). Everything runs on a single host with one GPU — observability, reproducibility, and safety are non-negotiable. Deterministic routing beats clever routing.

## Repo layout

```
hermes-apprentice/
├── observer/         Go    SQLite → normalized pairs
├── detector/         Go    BGE-small embedding + clustering
├── dataset-builder/  Go    Fetch → redact → quality → dedup → split
├── train/            Py    Unsloth QLoRA + manifest signing
├── validator/        Py    Baseline + promotion gate + registry
├── serving/          Py    vLLM HTTP + residency control plane
├── proxy/            Go    OpenAI-compat router (core: canary, tenants, ratelimit, patterns, alias)
├── orchestrator/     Py    Pipeline driver, MCP server, safety, budget, quota
├── telegram/         Py    Templates, outbox, reply poller
├── burst/            Go    RunPod A100 spot dispatcher
├── deploy/           YAML  Docker compose, Grafana, Prometheus
├── scripts/          Sh    Demo, helpers
├── tasks/            JSON  73-subtask tracker
├── notes/            Md    Research, runbooks
└── docs/             Md    Plans, benchmarks, migration guides
```

## Two venvs

Python packages use `uv` (Astral). There are **two venvs** because torch (training/validation) and non-torch (orchestrator/telegram) packages conflict:

```bash
# Train venv: torch + unsloth + vllm + transformers
uv venv venv-train --python 3.12
source venv-train/bin/activate
uv pip install -e trainer -e validator -e serving

# Orchestrator venv: lightweight, no torch
uv venv venv-orch --python 3.12
source venv-orch/bin/activate
uv pip install -e orchestrator -e telegram
```

Docker Compose is available as an alternative (`deploy/docker/docker-compose.yml`).

## Tests

```bash
make test          # Go + Python (all packages)
make coverage      # Coverage reports
make lint          # go vet all packages
```

### Go

```bash
cd proxy && go test ./... -v
```

10 internal packages tested: alias, canary, cost, embedder, httpapi, patterns, ratelimit, runpod, summary, tenant. Tests use `httptest.Server` for HTTP mocks and `os.TempDir` for file-based state.

### Python

```bash
cd orchestrator && pytest tests/ -v
```

Tests use `pytest` with `tmp_path`/`monkeypatch` fixtures. No external services needed — all HTTP calls mocked via `http.server.HTTPServer` or `unittest.mock`.

Test counts: orchestrator 101, trainer 57, validator 89, serving 40, telegram 41 = **328 total**.

## Commits

All commits follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

Types:  feat, fix, docs, chore, refactor, test, ci, build, perf
Scopes: foundation, observer, detector, dataset-builder, trainer, serving,
        validator, proxy, skill, instrumentation, telegram, demo, orchestrator
```

Examples:
- `feat(proxy): add canary ramp state machine`
- `test(orchestrator): add safety.py integration tests`
- `docs(readme): update architecture diagram for v0.2`

## Release process

1. Update `CHANGELOG.md` (keep `vNEXT` section, move to version on release)
2. Bump version in all `pyproject.toml` files
3. Tag: `git tag v0.2.0 -m "v0.2.0"`
4. Push: `git push origin main --tags`
5. Verify CI: `make test && make lint`
