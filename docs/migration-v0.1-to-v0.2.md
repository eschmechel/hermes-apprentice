# Migration Guide: v0.1 → v0.2

## Breaking changes

### Registry format

**v0.1** registry manifests stored `model_dir` and `scores`. **v0.2** adds `base_model` and optional `merged_from`:

```json
{
  "schema_version": 1,
  "pattern_id": "extract-json-from-text",
  "version": 3,
  "promoted_at": "2026-05-22T12:00:00Z",
  "model_dir": "/home/user/.apprentice/registry/extract-json-from-text/v3",
  "scores": {"exact_match": 0.85, "f1": 0.91},
  "source_training_manifest": "/home/user/.apprentice/registry/extract-json-from-text/v3/training_manifest.json",
  "base_model": "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit",
  "merged_from": [
    {"pattern_id": "parent-a", "version": 2},
    {"pattern_id": "parent-b", "version": 1}
  ]
}
```

**Action:** Existing v0.1 registries continue to work (optional fields are `omitempty`). No migration needed.

### Pattern storage format

**v0.1** `patterns.json` was a flat list. **v0.2** adds `tenant_id`:

```json
{
  "id": "extract-json-from-text",
  "description": "Extract structured JSON from unstructured text",
  "centroid": [0.12, -0.34, ...],
  "tenant_id": ""
}
```

Empty `tenant_id` = global pattern, visible to all tenants.

**Action:** Existing patterns without `tenant_id` are treated as global — no migration needed.

### CLI flags

| v0.1 flag | v0.2 change |
|-----------|-------------|
| `--base-model` hardcoded default | `--base-model` resolved from `supported_models.yaml` |
| `--profile` YAML files | Unchanged, but `base_model` key uses alias resolution |
| None | `--tenant`, `--proxy-url`, `--tenant-root`, `--global-api-key`, `--canary-*` added |

### Proxy serve flags (new)

```bash
proxy serve \
  --tenant-root ~/.apprentice/tenants \      # NEW: tenant auth
  --global-api-key "admin-secret" \           # NEW: admin bypass
  --tenant-ratelimit-rpm 60 \                 # NEW: rate limiting
  --canary-state-dir ~/.apprentice/canary \   # NEW: canary ramp
  --canary-ramp-start 5 \                    # NEW: start %
  --canary-ramp-step 10 \                    # NEW: step %
  --canary-ramp-step-requests 50 \           # NEW: reqs per step
  --canary-agreement-threshold 0.8           # NEW: agreement threshold
```

All flags are optional — the proxy works identically to v0.1 without them.

### Multi-tenant setup

```bash
# v0.2: tenants stored under ~/.apprentice/tenants/
mkdir -p ~/.apprentice/tenants/acme

# API key (32-byte hex)
echo "abc123def456..." > ~/.apprentice/tenants/acme/.apikey

# Quota (auto-created on first check)
apprentice-orchestrator quota set --tenant acme --max-loras 5 --training-hours 50

# Budget
apprentice-orchestrator budget set --tenant acme --monthly 100
```

### Directory structure (new)

```
~/.apprentice/
├── proxy/
│   ├── patterns.json          # v0.1 format, unchanged
│   ├── canary.json            # NEW: canary state
│   └── aliases.json           # NEW: pattern aliases
├── tenants/                   # NEW
│   └── <tenant>/
│       ├── .apikey
│       ├── quota.json
│       └── budget.jsonl
├── registry/                  # v0.1 format, extended
├── datasets/                  # unchanged
├── decisions/                 # unchanged
├── outbox/                    # unchanged
├── failures/                  # unchanged
└── keys/                      # unchanged
```

### Docker Compose

**v0.1:** `deploy/docker/docker-compose.yml` — proxy, serve, observer.
**v0.2:** unchanged core compose, plus `deploy/docker/compose.monitoring.yml` for Prometheus + Grafana.

### Python version

**v0.1:** `requires-python = ">=3.10,<3.13"`
**v0.2:** `requires-python = ">=3.10,<3.15"` — relaxed for Python 3.14 compatibility.
