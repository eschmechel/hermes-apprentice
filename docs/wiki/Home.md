# Apprentice v0.2

Hermes Agent accumulates Markdown skills from experience. Apprentice adds a second loop: when Hermes has handled a request pattern enough times, the Apprentice harvests `(input, big-model-output)` pairs from session history, fine-tunes a small Qwen2.5 specialist with Unsloth QLoRA, validates it against a held-out test set, and registers the result as a Hermes skill that routes future similar requests to a free local endpoint.

Skills today are prompts. Apprentice makes some of them weights.

## Pages

- [Architecture](Architecture) — System diagram, component roles, data flow
- [Quickstart](Quickstart) — Five-minute smoke test
- [Setup Guide](Setup-Guide) — Full installation walkthrough using `apprentice-setup`
- [Pipeline](Pipeline) — End-to-end: capture → detect → train → merge → validate → serve
- [Pattern Merging](Pattern-Merging) — Combining two specialists
- [Multi-Tenancy](Multi-Tenancy) — Tenant auth, quotas, rate limiting
- [Budget & Cost](Budget-&-Cost) — Monthly budget, thresholds, Telegram increase flow
- [Canary & Safety](Canary-&-Safety) — Safe rollout ramp, auto-quarantine
- [RunPod & Cloud](RunPod-&-Cloud) — Cloud GPU burst
- [Multi-Provider Upstream](Multi-Provider-Upstream) — OpenRouter + fallback chain
- [Monitoring](Monitoring) — Grafana dashboards, Prometheus metrics
- [API Reference](API-Reference) — Proxy endpoints, MCP tools
- [Operations](Operations) — Daily ops: cron jobs, logs, health checks
- [Known Gaps & Roadmap](Known-Gaps-&-Roadmap) — Current limitations and v0.3 plans
