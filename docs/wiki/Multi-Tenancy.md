# Multi-Tenancy

Apprentice v0.2 supports multiple tenants sharing a single deployment. Each tenant has isolated patterns, rate limits, quotas, and budgets.

## Architecture

```
Client → X-Apprentice-Tenant: acme
         X-Apprentice-Key: sk-...    →  Proxy (auth middleware)
                                          │
                                          ├─ Valid → tenant scoped
                                          ├─ Invalid → 401
                                          └─ No headers → anonymous (no rate limit)
```

## Tenant setup

### Initialize tenant storage

```bash
# Register tenants via the orchestrator
apprentice-orchestrator quota set --tenant acme --max-loras 5
apprentice-orchestrator quota set --tenant beta --max-loras 3 --max-vram-gb 24
apprentice-orchestrator budget set --tenant acme --monthly 100
apprentice-orchestrator budget set --tenant beta --monthly 50
```

Tenant data is stored at `~/.apprentice/tenants/{tenant}/`:

```
tenants/
├── acme/
│   ├── .apikey           # API key for authentication
│   ├── quota.json         # Resource limits (max LORAs, VRAM, training hours)
│   └── budget.jsonl       # Monthly spend ledger
└── beta/
    ├── .apikey
    ├── quota.json
    └── budget.jsonl
```

### Start proxy with tenant auth

```bash
proxy serve \
    --tenant-root ~/.apprentice/tenants \
    --tenant-ratelimit-rpm 60 \
    --global-api-key "admin-secret"
```

- `--tenant-root` — Directory containing per-tenant subdirectories.
- `--tenant-ratelimit-rpm` — Requests per minute per tenant (token bucket).
- `--global-api-key` — Admin key for global pattern management.

### Client headers

```bash
curl http://localhost:8083/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "X-Apprentice-Tenant: acme" \
    -H "X-Apprentice-Key: $(cat ~/.apprentice/tenants/acme/.apikey)" \
    -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'
```

## Global patterns

Patterns registered without a tenant (or with `X-Apprentice-Key` matching the global API key) are visible to all tenants. This allows sharing common specialists (e.g., "fix Python type errors") across tenants.

```bash
# Register a global pattern (requires global API key)
curl -X POST http://localhost:8083/patterns \
    -H "X-Apprentice-Key: admin-secret" \
    -H "Content-Type: application/json" \
    -d '{"id":"fix-python-types","description":"Fix type errors","centroid":[...]}'
```

Global patterns appear in `BestMatchTenant()` results alongside the tenant's own patterns. A tenant's own patterns take priority if the same centroid matches both (first match in tenant-scoped search wins).

## Rate limiting

Per-tenant token bucket, configurable RPM:

```bash
proxy serve --tenant-ratelimit-rpm 120    # 120 requests/min per tenant
```

Anonymous requests (no `X-Apprentice-Tenant` header) are **not** rate-limited when tenant root is unset. When tenant root is set and no tenant header is present, the request gets a 401.

## Quota system

Per-tenant resource limits managed by the orchestrator:

| Limit | Check |
|-------|-------|
| `max_loras` | Max active LoRA adapters |
| `max_vram_gb` | Max VRAM for training |
| `max_training_hours` | Monthly training hours |

```bash
# View quotas
apprentice-orchestrator quota list
apprentice-orchestrator quota get --tenant acme

# Set limits
apprentice-orchestrator quota set --tenant acme --max-loras 5 --max-training-hours 100

# Check before training
apprentice-orchestrator quota check --tenant acme
```

Quota counters reset at the start of each calendar month. The `check_quota()` function is called before any training job starts.

See [Budget & Cost](Budget-%26-Cost) for the monetary budget system.
