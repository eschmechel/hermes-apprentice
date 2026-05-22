# Changelog

## v0.2.0 (2026-05-22)

### Added

- **Canary ramp**: Gradual traffic rollout (5%→100%) with automatic agreement scoring and broken-state quarantine. `proxy/internal/canary/`.
- **Multi-base-model support**: Configurable training on Qwen2.5-1.5B (default), Qwen2.5-3B, Llama-3.2-3B via `trainer/supported_models.yaml`.
- **Pattern merging**: `dataset-builder merge` subcommand, alias store, regression gate against both parents, MCP `propose_merge` tool.
- **Multi-tenant auth**: `X-Apprentice-Tenant` header + API key validation, per-tenant rate limiting, tenant-scoped pattern matching, global patterns.
- **Quota system**: Per-tenant resource quotas (max LoRAs, training hours, VRAM). `orchestrator/quota.py`.
- **Monthly budget**: Monetary spend tracking with 80%/95%/100% Telegram alerts. On-demand increase via `budget increase $N` reply. `orchestrator/budget.py`.
- **Grafana dashboards**: Pre-built 8-panel dashboard (request rate, latency, error rate, cost, patterns, comparison, pie chart, counters).
- **RunPod burst**: Flash pod provisioning for cloud training (A100/A6000/L40S). `burst/` and `orchestrator/flash_burst.py`.
- **Orchestrator**: Autonomous pipeline driver with MCP tools, safety management, cost/ROI computation.
- **Safety CLI**: `apprentice-orchestrator safety list|status|advance|set-state|compare|alert` for canary management.
- **Cost dashboard**: `apprentice-orchestrator cost --roi|--usage|--latency` for proxy log analysis.
- **Unified Makefile**: `make test`, `make coverage`, `make lint` for all 4 Go modules and 5 Python packages.

### Changed

- Hardcoded base model `"unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"` replaced with `supported_models.yaml` resolution.
- Python constraint relaxed to `<3.15` for Python 3.14 compatibility.
- `Pattern` struct now includes `TenantID` for multi-tenant scoping.

## v0.1.0 (2026-05-18)

### Added

- Hermes microVM bootstrapping (Firecracker rootfs build).
- Observer: tails Hermes SQLite, normalizes conversation pairs.
- Detector: BGE-small ONNX embedding + HDBSCAN clustering → candidate patterns.
- Dataset builder: fetch → PII redact (Presidio) → quality filter → dedup → augment → split 80/10/10.
- Trainer: Unsloth QLoRA fine-tuning with GPU profiles (A100, 2080 Ti, 4060 Mobile).
- Model export: LoRA merge to fp16 merged_16bit.
- Manifest signing: Ed25519 key pairs for training and registry manifests.
- Validator: baseline runner (vLLM offline batch) + promotion gate (10pp margin).
- Registry: versioned model storage with signed manifests.
- Skill registrar: Hermes SKILL.md rendering + staging + scp push.
- Serving: vLLM HTTP server with multi-LoRA residency control plane.
- Proxy: OpenAI-compatible router with BGE-small embedding matching, 5% shadow sampling, Prometheus metrics, rolling latency stats.
- Registry service: read-only HTTP API over registry directory.
- Telegram: graduation/failure/weekly templates, file-queue outbox, reply poller.
- Systemd unit template for serving (`apprentice-vllm@.service`).
- Docker Compose (app + dependencies).
- 73-subtask JSON tracker across 12 milestones.
