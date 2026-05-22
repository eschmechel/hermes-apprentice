# Apprentice v0.2

Hermes Agent has skills — Markdown files that tell it how to handle a request.
Apprentice adds a second loop: when Hermes has seen a pattern enough times,
Apprentice grabs the user/model pairs, fine-tunes a little Qwen2.5 on them,
validates the result, and registers it as a skill that routes future matches
to a free local endpoint.

Skills are prompts. **Apprentice turns some of them into weights.**

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
