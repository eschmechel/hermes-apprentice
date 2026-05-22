# Pattern Merging

Combine two specialist patterns into a single merged one. Useful when two patterns are discovered to be variants of the same underlying skill.

## Flow

```
Operator proposes merge (MCP or CLI)
       │
       ▼
  dataset-builder merge      ← fetches both datasets, combines, deduplicates
       │
       ▼
  Telegram notification       ← "Merge candidate ready. Reply 'train gc-...'"
       │
       ▼
  Operator approves           ← train gc-...
       │
       ▼
  Orchestrator runs pipeline  ← train → merge → validate with regression
       │
       ▼
  Regression gate             ← must beat BOTH parents independently
       │
       ▼
  Registry promote            ← writes merged_from lineage + alias
       │
       ▼
  Proxy alias                  ← parent IDs route to merged specialist
```

## Proposing a merge

### Via MCP (conversational)

```
propose_merge(parent_a="pattern-a", parent_b="pattern-b",
              merged_id="pattern-merged", description="Combined email extraction")
```

### Via CLI

```bash
dataset-builder merge --pattern-a <id1> --pattern-b <id2> --output-dir ~/.apprentice/datasets/merged
```

The merge subcommand:
1. Fetches records for both patterns from the observer.
2. Redacts PII on both datasets independently.
3. Quality-filters both.
4. Combines and deduplicates the combined set.
5. Splits 80/10/10.
6. Version-saves to `~/.apprentice/datasets/<merged_id>/v1/`.
7. Writes `data_card.json` with lineage (a_id, b_id, merged_from versions).

## Training

Standard pipeline with an additional regression check:

```bash
apprentice-validate --model-dir <merged> --test-dataset <test.jsonl.gz> \
    --pattern-id <merged-id> \
    --merge-regression <parent-a-id> <parent-b-id>
```

The regression gate runs the merged specialist against each parent's test set. The merged model must pass the promotion gate for **both** parents independently (+10pp exact-match and F1 over each parent's baseline).

## Registry manifest

On pass, the registry manifest records lineage:

```json
{
  "schema_version": 1,
  "pattern_id": "pattern-merged",
  "version": 1,
  "base_model": "Qwen/Qwen2.5-1.5B-Instruct",
  "merged_from": [
    {"pattern_id": "pattern-a", "version": 3},
    {"pattern_id": "pattern-b", "version": 1}
  ],
  "aliases": ["pattern-a", "pattern-b"]
}
```

## Proxy routing

Aliases are registered in the proxy so requests matching either parent are routed to the merged specialist:

```bash
# Extract centroids from parents and point them at the merged ID
curl -X POST http://localhost:8083/aliases -d '{"pattern_id":"pattern-a","target":"pattern-merged"}'
curl -X POST http://localhost:8083/aliases -d '{"pattern_id":"pattern-b","target":"pattern-merged"}'
```

The proxy's alias store resolves both parent IDs to the merged specialist after cosine matching.

## Safety

- Merging requires operator approval via Telegram — cannot be automated.
- Regression gate runs against both parents independently — merged model must not regress on either.
- If either parent test fails, the merge is blocked and a failure report is written.
- Original parent specialists remain intact in the registry.
