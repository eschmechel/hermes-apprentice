# Setup Guide

`apprentice-setup` is the all-in-one installer. It detects your host, collects API keys, builds Go binaries, reproduces Python virtual environments, and writes a persistent environment config — all in one command.

## Prerequisites

| Requirement | Why |
|-------------|-----|
| Linux host with KVM + tun/tap | Required for Firecracker microVM |
| Go 1.26+ | Builds proxy, dataset-builder, registry-service, burst |
| Python 3.10+ | Trainer, validator, serving, orchestrator, telegram, installer |
| `uv` (Astral) | Creates and manages two virtual environments |
| Docker | For Presidio PII redaction sidecar and optional monitoring |
| NVIDIA GPU + CUDA | For training/inference; **or** RunPod account for cloud burst |
| Telegram bot token + chat ID | Optional — for graduation notifications and operator commands |

## Interactive setup (recommended)

```bash
# Hermes microVM first (if using Firecracker profile)
bash .firecracker/bootstrap.sh
.firecracker/vm.sh start

# Run the installer — detects GPU/KVM/Docker/uv, prompts for keys
apprentice-setup --apply
```

`apprentice-setup --apply` walks through:

1. **Environment detection** — GPU (nvidia-smi), KVM (/dev/kvm), Docker, uv, Go.
2. **Isolation profile** — Recommends Firecracker (bare-metal KVM) or Docker (VM / no KVM).
3. **API keys** — Telegram bot token + chat ID, OpenRouter API key, RunPod API key (all skippable).
4. **Base model** — Choose from Qwen2.5-1.5B (default), Qwen2.5-3B, or Llama-3.2-3B.
5. **Monthly budget** — Default $20 (0 = local-only, no cloud spend).
6. **Admin API key** — Auto-generated for multi-tenant global pattern management.
7. **Go binaries** — Builds proxy, dataset-builder, registry-service, burst into `~/.apprentice/bin/`.
8. **Python venvs** — Reproduces `venv-train` and `venv-serve` from lockfiles.
9. **Cron lines** — Prints `crontab` entries for autonomous operation.
10. **Monitoring** — Optional: starts Prometheus + Grafana via Docker.

Dry-run by default — omit `--apply` to preview without executing.

## Scripted / CI setup

```bash
apprentice-setup --apply --non-interactive \
    --profile docker \
    --telegram-token "$BOT_TOKEN" \
    --telegram-chat-id "$CHAT_ID" \
    --openrouter-key "$OPENROUTER_KEY" \
    --runpod-key "$RUNPOD_KEY" \
    --base-model qwen2.5-1.5b \
    --monthly-budget 20 \
    --global-api-key "my-admin-secret" \
    --enable-monitoring
```

All flags are optional — only the ones you provide are written to `~/.apprentice/.env`.

## What the installer creates

```
~/.apprentice/
├── .env                    # Persistent config (all key/value pairs)
├── bin/                    # Go binaries
│   ├── proxy
│   ├── dataset-builder
│   ├── registry-service
│   └── burst
├── venv-train/             # uv virtual environment (trainer)
├── venv-serve/             # uv virtual environment (validator, serving, orchestrator, telegram)
├── tenants/                # Per-tenant API keys and quotas
├── keys/                   # Ed25519 signing keys
├── proxy/
│   ├── patterns.json
│   └── canary/             # Canary ramp state (survives restarts)
├── registry/               # Promoted specialist models
├── datasets/               # Versioned training datasets
├── cost/
│   └── ledger.jsonl        # Training cost ledger
├── failures/               # Validation failure reports
└── decisions/              # Telegram reply decision markers
```

## Post-install verification

```bash
# Health check
curl http://localhost:8083/healthz

# List available models
apprentice-trainer --list-models

# Run demo (end-to-end offline test)
bash scripts/demo-run.sh
```

## Manual alternative

If you prefer step-by-step control over each component:

```bash
# Python packages (editable installs)
for pkg in trainer validator serving telegram orchestrator installer; do
  ( cd "$pkg" && uv pip install -e . )
done

# Go binaries
for pkg in observer detector dataset-builder proxy registry-service burst; do
  ( cd "$pkg" && go build ./... && go install ./... )
done

# Signing key
apprentice-trainer-keygen ~/.apprentice/keys
```

## Next steps

- [Quickstart](Quickstart) — Five-minute smoke test
- [Pipeline](Pipeline) — Drive the full training pipeline
- [Operations](Operations) — Cron jobs, logs, health checks
