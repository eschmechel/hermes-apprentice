# Canary & Safety

Safe specialist rollout using a canary ramp state machine. New specialists start at 5% traffic and auto-advance as agreement scores prove they match upstream quality.

## States

| State | Behavior |
|-------|----------|
| **warming** | Routes `pct%` of matched requests to specialist (e.g., 5%). Remaining requests go upstream. Every request shadows upstream for comparison. |
| **live** | 100% of matched requests routed to specialist. No shadowing. |
| **broken** | 0% routed to specialist. All matched requests go upstream. Requires manual intervention to recover. |

## Ramp mechanics

```
new specialist → warming (5%)
       │
       ├─ every 50 shadow comparisons:
       │   average agreement ≥ 0.80 → step up +10%
       │
       ├─ 5% → 15% → 25% → ... → 95% → live (100%)
       │
       └─ average agreement < 0.80 → broken (0%, quarantined)
```

### Configuration

```bash
proxy serve \
    --canary-state-dir ~/.apprentice/proxy/canary \
    --canary-ramp-start 5 \
    --canary-ramp-step 10 \
    --canary-ramp-step-requests 50 \
    --canary-agreement-threshold 0.8
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--canary-ramp-start` | 5 | Initial traffic percentage for new specialists |
| `--canary-ramp-step` | 10 | Percentage increase per successful evaluation |
| `--canary-ramp-step-requests` | 50 | Shadow comparisons per evaluation step |
| `--canary-agreement-threshold` | 0.80 | Minimum Jaccard token-overlap score to advance |

## Agreement scoring

The proxy compares specialist and upstream responses using **Jaccard token overlap** on the extracted content:

```go
func CompareResponses(specialist, upstream []byte) float64 {
    sTokens := tokenize(ExtractContent(specialist))
    uTokens := tokenize(ExtractContent(upstream))
    intersection := len(intersect(sTokens, uTokens))
    union := len(union(sTokens, uTokens))
    return float64(intersection) / float64(union)
}
```

## Auto-advance and auto-demote

The proxy's `finishCanaryShadow` goroutine runs after every shadowed request:

1. Records the agreement score against the pattern's canary state.
2. When `request_count >= step_requests`, evaluates the average agreement.
3. If `avg >= threshold` and `pct < 100` → advance to next step.
4. If `avg >= threshold` and `pct >= 100` → transition to `live`.
5. If `avg < threshold` → transition to `broken` (quarantine).

Broken specialists trigger a Telegram alert: "⚠️ Canary: pattern 'X' demoted to broken."

## HTTP API

```bash
# List all canary states
curl http://localhost:8083/canary/state

# Get a specific pattern
curl http://localhost:8083/canary/state/<pattern-id>

# Manually advance (with explicit score)
curl -X POST http://localhost:8083/canary/advance \
    -d '{"pattern_id":"p1","score":0.85}'

# Manually override state
curl -X POST http://localhost:8083/canary/set-state \
    -d '{"pattern_id":"p1","state":"live","pct":100}'

# Compare two response bodies
curl -X POST http://localhost:8083/canary/compare \
    -d '{"specialist":"...","upstream":"..."}'
```

## Python CLI (orchestrator)

```bash
apprentice-orchestrator safety list
apprentice-orchestrator safety status --pattern-id <id>
apprentice-orchestrator safety advance --pattern-id <id> --score 0.85
apprentice-orchestrator safety set-state --pattern-id <id> --state live --pct 100
apprentice-orchestrator safety alert --pattern-id <id>
```

## Persistence

Canary state is persisted to JSON (`~/.apprentice/proxy/canary/canary_state.json`). Survives proxy restarts. All shadow comparison data resets on restart — the ramp continues from the last saved percentage.

## Recovery from broken

A broken specialist must be manually fixed:

1. Investigate the failure (check agreement scores, compare responses).
2. Re-train or fix the issue.
3. Manually set state back to `warming`:
   ```bash
   curl -X POST http://localhost:8083/canary/set-state \
       -d '{"pattern_id":"p1","state":"warming","pct":5}'
   ```
4. The ramp auto-advances from there.

See [Monitoring](Monitoring) for Grafana dashboards that visualize canary state transitions.
