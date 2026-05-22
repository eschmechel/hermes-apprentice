# RunPod & Cloud

Cloud GPU burst when local hardware is unavailable or insufficient. Budget-gated provisioning through RunPod's GraphQL API.

## GPU types

| GPU | VRAM | Price/sec | Price/hr | Use case |
|-----|------|-----------|----------|----------|
| **A100** | 80 GB | $0.00076 | ~$2.74 | 3B models, large batches |
| **A6000** | 48 GB | $0.00034 | ~$1.22 | 1.5B models, medium batches |
| **L40S** | 48 GB | $0.00032 | ~$1.15 | 1.5B models, cost-optimized |

## How it works

```
Orchestrator needs to train, local GPU busy/absent
       │
       ▼
  placement.decide()
       │
       ├─ Local GPU available? → use local
       │
       └─ No → flash_burst.can_burst()
             │
             ├─ Budget ok? → provision RunPod pod
             ├─ Quota ok? → provision RunPod pod
             ├─ API key set? → provision RunPod pod
             │
             └─ Any fail? → skip, log, move to next job
```

## CLI

```bash
# Check if bursting is available
apprentice-orchestrator burst check --gpu A100

# List available GPU types
apprentice-orchestrator burst list

# Provision a pod
apprentice-orchestrator burst provision --gpu A100

# Terminate a pod
apprentice-orchestrator burst terminate --pod-id <id>

# List running pods
apprentice-orchestrator burst list-pods
```

## Budget gate

Every provision and training run is gated by `check_budget()`:

```python
def can_burst(self, tenant_id, gpu_type):
    # 1. Check RUNPOD_API_KEY is set
    # 2. Check monthly budget has remaining capacity
    # 3. Check quota (max_training_hours, max_loras)
    # 4. Estimate cost based on GPU type and typical training duration
    # 5. Return bool + error message
```

The estimated cost for a typical training run is pre-computed and checked against the remaining budget. If the estimated cost would exceed the budget, the provision is blocked.

## Go proxy client (`proxy/internal/runpod/client.go`)

The Go proxy has a RunPod client for live pod cost tracking on the dashboard:

```go
// List all pods with their costs
pods := client.ListPods(ctx)

// Provision a new pod
result := client.ProvisionPod(ctx, input)

// Terminate a pod
client.TerminatePod(ctx, podID)

// Ping for connection check
client.Ping(ctx)
```

Used by the `GET /api/cost/runpod` endpoint on the proxy.

## Docker Compose

The `burst` service in `docker-compose.yml` handles RunPod job dispatching:

```yaml
burst:
  build:
    context: ..
    dockerfile: deploy/docker/Dockerfile.burst
  environment:
    - RUNPOD_API_KEY=${RUNPOD_API_KEY}
```

## Pod lifecycle

1. **Provision** — Pod created via `podFindAndDeployOnDemand` GraphQL mutation.
2. **Execute** — Training job runs on the pod (LoRA fine-tune, typically 5-15 min).
3. **Record** — Cost recorded to `budget.jsonl` and training hours to quota.
4. **Terminate** — Pod destroyed via `podTerminate` mutation.

Pods are spot instances and may be preempted. The orchestrator retries on provision failure.

See [Budget & Cost](Budget-&-Cost) for the budget system and [Multi-Tenancy](Multi-Tenancy) for per-tenant quotas.
