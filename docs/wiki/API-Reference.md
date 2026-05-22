# API Reference

## Proxy HTTP API (`localhost:8083`)

### Core route

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat completions. Embed → match → route. Headers: `X-Apprentice-Tenant`, `X-Apprentice-Key`, `Authorization` (forwarded to upstream). |

### Patterns

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/patterns` | Tenant | Register a new pattern (`id`, `description`, `centroid`) |
| `GET` | `/patterns` | Tenant | List patterns for the authenticated tenant |

### Aliases

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/aliases` | Register an alias (`pattern_id`, `target`) |
| `GET` | `/aliases` | List all aliases |
| `GET` | `/aliases/{pattern_id}` | Resolve an alias to its target |
| `DELETE` | `/aliases/{pattern_id}` | Remove an alias |

### Canary

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/canary/state` | List all canary ramp states |
| `GET` | `/canary/state/{pattern_id}` | Get state for a specific pattern |
| `POST` | `/canary/advance` | Trigger evaluation (`pattern_id`, optional `score`) |
| `POST` | `/canary/set-state` | Override state (`pattern_id`, `state`, `pct`) |
| `POST` | `/canary/compare` | Compare two response bodies (`specialist`, `upstream`) |

### Cost & ROI

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/cost/roi` | Full ROI snapshot for all patterns |
| `GET` | `/api/cost/roi/{pattern_id}` | ROI for a specific pattern |
| `GET` | `/api/cost/usage?pattern_id=&bucket=hour\|day\|week` | Usage over time |
| `GET` | `/api/cost/latency` | Average latency (specialist vs upstream) |
| `GET` | `/api/cost/runpod` | RunPod live pod costs |

### Registry

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/registry/{pattern_id}/latest` | Get latest version manifest for a pattern |

### Observability

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Health check (returns 200) |
| `GET` | `/stats` | Rolling p50/p95/p99 latency percentiles |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/dashboard` | Vue.js + Chart.js SPA dashboard |

## Registry Service HTTP API (`localhost:8082`)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/registry/{skillId}/latest` | Get the latest promoted version for a skill |
| `GET` | `/healthz` | Health check |

## Serving Control Plane HTTP API (`localhost:8071`)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/residency/ensure` | Load adapter if not resident (called by proxy) |
| `POST` | `/residency/pin` | Pin adapter (never evict) |
| `POST` | `/residency/unpin` | Unpin adapter |
| `POST` | `/residency/evict` | Explicitly unload adapter |
| `GET` | `/residency/status` | List resident + pinned adapters |
| `GET` | `/healthz` | Health check |

## Observer HTTP API (`localhost:8081`)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/records?pattern=<id>&since=<timestamp>&limit=<n>` | Fetch normalized conversation pairs |

## MCP Tools (orchestrator)

Available when running `apprentice-orchestrator-mcp`:

| Tool | Parameters | Description |
|------|------------|-------------|
| `dispatch_training` | `pattern_id`, `dataset_dir?` | Queue a training job |
| `job_status` | `job_id?` | Get status of training job(s) |
| `roi` | `pattern_id` | Compute return on investment |
| `propose_merge` | `parent_a`, `parent_b`, `merged_id?`, `description` | Propose merging two patterns |
| `list_tenants` | — | List all registered tenants |
| `get_quota` | `tenant` | Get quota for a tenant |
| `set_quota` | `tenant`, `max_loras?`, `max_vram_gb?`, `max_training_hours?` | Set quota limits |
| `get_budget` | `tenant` | Get budget status |
| `set_budget` | `tenant`, `monthly_budget_usd` | Set monthly budget |
| `budget_increase` | `tenant`, `amount` | Add funds to monthly budget |
| `budget_history` | `tenant` | Get budget spending history |

## CLI Commands

### `apprentice-setup` (installer)

All-in-one interactive installer. See [Setup Guide](Setup-Guide) for full flags.

### `apprentice-orchestrator`

| Subcommand | Description |
|------------|-------------|
| `tick` | Process pending decisions and drain request queue |
| `run` | Run the pipeline for a single pattern ID |
| `notify` | Enqueue a graduation notification |
| `cron` | Print recommended cron lines |
| `list-models` | List available base models |
| `cost` | Cost analysis (--roi, --usage, --latency) |
| `safety` | Canary management (list, status, advance, set-state, compare, alert) |
| `quota` | Quota management (list, get, set) |
| `budget` | Budget management (get, set, increase, history) |
| `burst` | RunPod burst control (check, list, provision, terminate, list-pods) |

### `apprentice-telegram`

| Subcommand | Description |
|------------|-------------|
| `enqueue` | Enqueue a message (graduation, failure, weekly) |
| `dispatch-one` | Deliver the oldest outbox message |
| `poll-replies` | Poll Telegram for operator replies |
| `list` | List outbox contents |
