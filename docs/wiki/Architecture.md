# Architecture

Apprentice is a multi-component system that captures, trains, and serves specialist language models for Hermes Agent.

## System diagram

```
┌─ Firecracker microVM ─────────────────────────────────────┐
│  Hermes Agent v0.14.0                                      │
│  - Chat endpoint → proxy:8083/v1                           │
│  - Cron: telegram-dispatch, telegram-poll-replies           │
└────────────────────────────────────────────────────────────┘
                          │
         ┌────────────────┼────────────────┐
         ▼                ▼                ▼
┌─────────────┐  ┌──────────────┐  ┌──────────────┐
│   Observer   │  │   Detector   │  │    Proxy     │
│  Go (port    │  │  Go (BGE-    │  │  Go (port    │
│  8081)       │  │  small ONNX) │  │  8083)       │
│  tails       │  │  HDBSCAN     │  │  embed→match │
│  state.db    │  │  clustering  │  │  →route      │
└─────┬────────┘  └──────┬───────┘  └──────┬───────┘
      │                  │                  │
      ▼                  ▼                  │
┌─────────────┐  ┌──────────────┐           │
│  Telegram    │  │ Orchestrator │           │
│  Bot         │  │  Python      │           │
│  reply       │  │  watcher     │           │
│  poller      │─▶│  pipeline    │           │
└─────────────┘  └──────┬───────┘           │
                         │                   │
         ┌───────────────┼───────────┐       │
         ▼               ▼           ▼       │
┌─────────────┐  ┌──────────────┐  ┌──────────────┐
│  Dataset     │  │   Trainer    │  │    Serving    │
│  Builder     │  │   Python     │  │   Python      │
│  Go          │  │   Unsloth    │  │   vLLM HTTP   │
│  fetch→      │  │   QLoRA      │  │   residency   │
│  redact→     │  │              │  │   control      │
│  dedup→      │  │              │  │   plane        │
│  split       │  │              │  │                │
└─────────────┘  └──────┬───────┘  └────────────────┘
                         │
                         ▼
                ┌──────────────┐
                │   Validator  │
                │   Python     │
                │   baseline→  │
                │   promote    │
                └──────┬───────┘
                       │
                       ▼
              ┌──────────────┐
              │   Registry   │
              │   Go (port    │
              │   8082)       │
              │   read-only   │
              │   HTTP        │
              └──────────────┘
```

## Component roles

| Component | Language | Role |
|-----------|----------|------|
| **Observer** | Go | Tails `~/.hermes/state.db`, normalizes chat pairs |
| **Detector** | Go | BGE-small embedding + HDBSCAN clustering → pattern candidates |
| **Dataset Builder** | Go | Fetches records, redacts PII, filters quality, deduplicates, splits 80/10/10 |
| **Trainer** | Python | Unsloth QLoRA fine-tuning + LoRA merge + manifest signing |
| **Validator** | Python | Baseline comparison, promotion gate, registry management |
| **Serving** | Python | vLLM HTTP server + multi-LoRA residency control plane |
| **Proxy** | Go | Deterministic router: embed → cosine match → route to specialist or upstream fallback |
| **Orchestrator** | Python | Autonomous pipeline driver, MCP tools, budget/quota/safety management |
| **Telegram** | Python | Outbox templates + reply poller for operator approval flow |
| **Installer** | Python | All-in-one setup: detect host, build Go binaries, reproduce Python venvs, write .env |
| **Registry Service** | Go | Read-only HTTP API over `~/.apprentice/registry/` |
| **Burst** | Go | RunPod A100 spot dispatcher for cloud GPU training |

## Data flow

1. **Capture** — Observer tails Hermes' SQLite DB, normalizes `(user_msg, assistant_reply)` pairs.
2. **Detect** — Detector embeds user messages (BGE-small), clusters with HDBSCAN, emits pattern candidates.
3. **Approve** — Telegram bot sends graduation message to operator channel. Operator replies `train gc-...` to approve.
4. **Train** — Orchestrator watcher picks up decision marker, runs pipeline: dataset-builder → trainer → merge.
5. **Validate** — Baseline runner (base model) vs specialist (fine-tuned). Promotion gate: +10pp exact-match and F1.
6. **Register** — On pass: copy model to `~/.apprentice/registry/<id>/v<N>/`, sign manifest, push SKILL.md to Hermes guest.
7. **Serve** — Proxy embeds user message, cosine-matches against centroids, routes to specialist (free) or upstream (paid).
8. **Monitor** — Prometheus scrapes `/metrics`, Grafana dashboards visualize request rate, latency, cost, errors.

## Key design decisions

- **Deterministic routing** — Cosine similarity against centroids, not an ML model. Predictable, debuggable.
- **File-system coordination** — Telegram bot writes decision markers; orchestrator reads them. No direct API calls between them.
- **Atomic writes** — All persistent state uses temp-file + rename. Crash-safe by design.
- **Ed25519 signatures** — Every training and registry manifest is cryptographically signed.
- **Graceful degradation** — No embedder → pass-through. Specialist fails → upstream fallback. Observer down → no new patterns, proxy still routes.
- **Canary ramp** — Specialists start at 5% traffic, auto-advance to 100% as agreement scores prove safe, auto-demote if scores drop.
- **Budget gate** — Cloud spend tracked per tenant. 80% warns, 95% pauses, 100% blocks. Bypass only via Telegram `budget increase`.
