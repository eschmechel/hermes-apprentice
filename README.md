# Apprentice — Hermes Agent that trains its own specialists

Hermes Agent accumulates Markdown skills from experience. **Apprentice** adds
a second loop: when Hermes has handled a request pattern enough times, the
Apprentice harvests `(input, big-model-output)` pairs from session history,
fine-tunes a small Qwen2.5-1.5B specialist with Unsloth QLoRA, validates it
against a held-out test set, and registers the result as a Hermes skill that
routes future similar requests to a free local endpoint.

Skills today are prompts. **Apprentice makes some of them weights.**

Built for the [Hermes Agent Challenge](https://dev.to/challenges/hermes-agent-2026-05-15)
(May 15–31, 2026). Base model: Qwen2.5-1.5B-Instruct (Apache 2.0). Hermes
runs inside a Firecracker microVM; everything else runs on the host.

> ![graduation moment GIF — placeholder, recorded in `demo-02`](docs/assets/graduation.gif)

## Architecture

```mermaid
flowchart LR
    subgraph microVM["Firecracker microVM (Hermes)"]
        H[Hermes Agent<br/>v0.14.0]
        CRON[hermes cron<br/>no_agent jobs]
        H -.chats.-> P
    end

    subgraph Host["Host (Linux + 2080 Ti / RunPod burst)"]
        subgraph Capture["1. Capture"]
            DB[(~/.hermes/state.db<br/>SQLite WAL)]
            O[Observer<br/>Go]
            DET[Pattern Detector<br/>Go + BGE-small ONNX]
            DB --> O --> DET
        end

        subgraph Train["2. Train"]
            DSB[Dataset Builder<br/>Go + Presidio]
            TR[Trainer<br/>Unsloth QLoRA]
            EXP[Merge & Export<br/>fp16 merged_16bit]
            DET --> DSB --> TR --> EXP
        end

        subgraph Validate["3. Validate & Promote"]
            BL[apprentice-baseline<br/>vLLM offline]
            VAL[apprentice-validate<br/>vLLM + gate]
            REG[(~/.apprentice/registry<br/>signed manifests)]
            EXP --> BL --> VAL --> REG
        end

        subgraph Serve["4. Serve"]
            SV[apprentice-serve<br/>vLLM HTTP]
            P[Proxy<br/>Go + 5% shadow]
            REG --> SV
            P --> SV
        end

        subgraph Notify["5. Notify"]
            OB[~/.apprentice/outbox]
            TG[apprentice-telegram<br/>+ Hermes deliver]
            DET --> OB
            VAL --> OB
            P --> OB
            OB --> TG
        end
    end

    P -- upstream fallback --> OR[(OpenRouter)]
    TG -.via Hermes cron.-> User[Telegram channel]
    CRON --> TG
    User -.replies.-> POLL[poll-replies<br/>getUpdates] --> DEC[(decisions/)]
    VAL --> SKILL[/.hermes/skills/&lt;id&gt;/SKILL.md]
    SKILL -.next session.-> H

    classDef done fill:#1f9d55,stroke:#0d6e3a,color:#fff;
    class O,DET,DSB,TR,EXP,BL,VAL,REG,SV,P,OB,TG,POLL,DEC,SKILL done;
```

Every coloured node is built and committed (see [Status](#status)). The
upstream API and Telegram channel are external.

### How a request flows through the proxy

1. Hermes' chat endpoint is configured to point at the local **Proxy**
   (`:8083/v1/chat/completions`).
2. The proxy embeds the last user message with **BGE-small ONNX** (384-dim,
   normalized) and compares it against every known pattern centroid by
   cosine.
3. Match above `--match-threshold` (default `0.78`) → forward to the local
   specialist via **apprentice-serve** (free, ~38ms).
4. No match or specialist returns non-2xx / empty `choices` → fall back to
   the upstream `--upstream-url` (OpenRouter, paid).
5. **5% shadow rate**: when the specialist handles a turn, a concurrent
   upstream call fires too so we can diff quality offline.

Every routed turn emits a structured JSON log line with `route_decision`,
`pattern_id`, `latency_ms`, `prompt_tokens`, `completion_tokens`,
`estimated_cost_usd`, and `cost_saved_usd`. Prometheus counters at
`/metrics`; rolling p50/p99 at `/stats`.

## Repo layout

```
hermes-apprentice/
├── observer/             — Go.   Tails ~/.hermes/state.db, normalises pairs.
├── detector/             — Go.   BGE-small ONNX → HDBSCAN → candidate patterns.
├── dataset-builder/      — Go.   Fetches pairs, redacts PII, splits 80/10/10.
├── trainer/              — Py.   Unsloth QLoRA on Qwen2.5-1.5B + manifest signer.
├── validator/            — Py.   apprentice-baseline (vLLM batch) + apprentice-validate
│                                 (promotion gate, signed registry, SKILL.md push).
├── serving/              — Py.   apprentice-serve wraps `vllm serve`.
├── proxy/                — Go.   OpenAI-compat router + 5% shadow + /stats /metrics.
├── registry-service/     — Go.   Read-only HTTP over ~/.apprentice/registry/.
├── telegram/             — Py.   Templates + outbox + getUpdates reply poller.
├── burst/                — Go.   RunPod A100 spot dispatcher (signed jobs).
├── tasks/                — JSON. 73-subtask plan, status flipped per milestone.
├── notes/                — Md.   Hermes source reads, integration runbooks.
└── docs/                 — Md.   Feasibility plan + session export.
```

## Quickstart

### Prerequisites

- Linux host with KVM and tun/tap (for Firecracker).
- Go 1.22+, Python 3.10–3.12, `uv` (Astral), Docker (for Presidio sidecar).
- An NVIDIA GPU for training (2080 Ti 11GB is enough for QLoRA) **or** a
  RunPod account for cloud burst.
- A Telegram bot token + chat id if you want notifications.

### One-time setup

```bash
# Hermes microVM (~10 min, see .firecracker/ for the rootfs build):
bash .firecracker/bootstrap.sh
.firecracker/vm.sh start

# Host Python packages — all editable, all uv-managed:
for pkg in trainer validator serving telegram; do
  ( cd "$pkg" && uv pip install -e . )
done

# Host Go binaries:
for pkg in observer detector dataset-builder proxy registry-service burst; do
  ( cd "$pkg" && go build ./... && go install ./... )
done

# Apprentice trainer key (used to sign training & registry manifests):
apprentice-trainer-keygen ~/.apprentice/keys
```

### Drive the pipeline

```bash
# 1. Capture: tail Hermes' SQLite into ~/.apprentice/observer/observer.db
observer serve --listen :8081 --hermes-db ~/.hermes/state.db &

# 2. Detect: every 15 min, embed + cluster recent inputs, write pattern manifests
detector serve --listen :8082 --observer-url http://localhost:8081 &

# 3. Build the dataset for a graduated pattern:
dataset-builder build --pattern-id <id> --observer-url http://localhost:8081 \
    --out ~/.apprentice/datasets/<id>/v1/

# 4. Train (local 2080 Ti or burst to RunPod):
apprentice-train --pattern-id <id> \
    --dataset ~/.apprentice/datasets/<id>/v1/train.jsonl.gz \
    --output  ~/.apprentice/checkpoints/<id>/v1/ \
    --profile rtx-2080-ti

apprentice-merge --base-model unsloth/Qwen2.5-1.5B-Instruct \
    --adapter-dir ~/.apprentice/checkpoints/<id>/v1/lora-adapter \
    --output-dir  ~/.apprentice/merged/<id>/v1/

# 5. Baseline (once per dataset/base-model pair):
apprentice-baseline --test-dataset ~/.apprentice/datasets/<id>/v1/test.jsonl.gz \
    --output ~/.apprentice/baselines/<id>-v1.jsonl

# 6. Validate + promote:
apprentice-validate --pattern-id <id> \
    --model-dir     ~/.apprentice/merged/<id>/v1 \
    --test-dataset  ~/.apprentice/datasets/<id>/v1/test.jsonl.gz \
    --baseline-pairs ~/.apprentice/baselines/<id>-v1.jsonl

# 7. Serve the promoted model:
apprentice-serve --model-dir ~/.apprentice/registry/<id>/latest/ \
    --port 8000 --gpu-memory-utilization 0.85 &

# 8. Proxy in front of Hermes:
proxy serve --listen :8083 --upstream-url https://openrouter.ai/api/v1

# Point Hermes' chat endpoint at http://10.0.2.1:8083/v1/chat/completions.
```

### Telegram (optional but slick)

```bash
# Enqueue a graduation message when the detector promotes a pattern:
apprentice-telegram enqueue graduation --pattern-id <id> \
    --record-count 42 --description "Extract SKU + qty from order emails." \
    --example "Order #4421 — confirm SKU AX-7 qty 3."

# Inside the microVM, register the dispatcher + poller as Hermes cron jobs:
scp telegram/scripts/apprentice-telegram-*.sh root@10.0.2.2:/root/.hermes/scripts/
ssh root@10.0.2.2 'hermes cron create --name apprentice-telegram \
    --no-agent --script apprentice-telegram-dispatch.sh \
    --deliver telegram "every 5m"'
ssh root@10.0.2.2 'hermes cron create --name apprentice-poll-replies \
    --no-agent --script apprentice-telegram-poll.sh "every 1m"'
```

User replies (`train gc-abcd1234`, `details gc-abcd1234`, `skip gc-abcd1234`)
land as JSON decision markers under `~/.apprentice/decisions/`. See
[`notes/telegram-integration.md`](notes/telegram-integration.md).

## Status

| Milestone | Subtasks | Done |
|---|---|---|
| foundation | 8 | 8 |
| observer | 6 | 6 |
| detector | 7 | 7 |
| dataset-builder | 9 | 9 |
| trainer | 8 | 8 |
| serving | 5 | 5 |
| validator | 8 | 8 |
| proxy | 9 | 9 |
| skill | 5 | 5 |
| instrumentation | 4 | 4 |
| telegram | 6 | 6 |
| demo | 7 | 1 (this README) |

Full plan: [`docs/apprentice-task-breakdown-2026-05-18-approved.md`](docs/apprentice-task-breakdown-2026-05-18-approved.md).
Per-milestone integration notes: [`notes/`](notes/).

## Design choices worth knowing

- **Hermes lives in a microVM, never on the host.** Skill files are written
  to a host staging dir then `scp`'d into `/root/.hermes/skills/<id>/`. See
  [`notes/skill-registration.md`](notes/skill-registration.md).
- **The proxy is the deterministic router; the SKILL.md is for ecosystem
  visibility.** Hermes' skill selector is LLM-judged and not reliable
  enough to be the only path. The skill body literally tells the agent
  "the proxy already routed this turn — respond normally".
- **Baseline is split from validate** (Option G). They run as separate
  CLIs against a file seam so two LLMs never share GPU memory.
- **Outgoing Telegram rides Hermes' `--deliver telegram` cron adapter**
  proven by `notes/foundation-07/proof.md`. We don't ship
  python-telegram-bot on the host.
- **Every training & registry manifest is Ed25519-signed.** The validator
  refuses to evaluate (`exit code 2`) an unsigned/forged training
  manifest, and the proxy can verify registry manifests offline.

## License

Apache 2.0. Qwen2.5-1.5B-Instruct is Apache 2.0 too — clean for portfolio
and contest submission.
