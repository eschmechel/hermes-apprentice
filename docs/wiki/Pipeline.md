# Pipeline

The end-to-end specialist training pipeline, from conversation capture to deployed specialist.

## Overview

```
Capture → Detect → Approve → Build Dataset → Train → Merge → Validate → Promote → Serve
```

The pipeline is driven by the **Orchestrator**, which is triggered by Telegram operator approvals or MCP dispatch requests.

## Step by step

### 1. Capture (observer)

```bash
observer serve --listen :8081 --hermes-db ~/.hermes/state.db
```

Tails Hermes' SQLite database in real time. Normalizes conversations into `(system_prompt, user_msg, assistant_reply)` triples. Exposes `GET /records?pattern=<id>&since=<timestamp>&limit=1000`.

### 2. Detect (detector)

```bash
detector serve --listen :8082 --observer-url http://localhost:8081
```

Embeds user messages with BGE-small ONNX (384-dim). Clusters with HDBSCAN. When a cluster reaches minimum density, emits a pattern candidate with description and centroid.

### 3. Approve (telegram + orchestrator)

The detector's pattern candidates flow to the Telegram bot. The operator receives a graduation message:

```
[gc-abcd1234] Graduation candidate ready.
  62 records available for "Extract shipping tracking numbers from customer emails".
  Reply "train gc-abcd1234" to train a specialist.
  Reply "skip gc-abcd1234" to archive.
```

Operator replies `train gc-abcd1234`. The Telegram reply poller writes a decision marker to `~/.apprentice/decisions/`. The orchestrator's `watcher.tick()` picks it up.

```bash
# Run the watcher (typically via cron)
apprentice-orchestrator tick
```

### 4. Build dataset (dataset-builder)

The orchestrator runs:

```bash
dataset-builder build --pattern-id <id> --observer-url http://localhost:8081
```

Fetches all records for the pattern. Pipeline:
1. **PII Redaction** — Microsoft Presidio analyzer replaces names, emails, phones with `<ENTITY_TYPE>` tags.
2. **Quality filter** — Drops re-asks ("could you clarify") and corrections ("no, I meant...").
3. **Fuzzy dedup** — Bigram Jaccard similarity (threshold 0.85), keeps first occurrence.
4. **Teacher augmentation** — If dataset < 200 records, OpenRouter LLM generates paraphrases.
5. **Split** — Deterministic 80% train / 10% val / 10% test.
6. **Versioned save** — `~/.apprentice/datasets/<id>/v<N>/` with manifest and SHA-256 hash.

Output: `train.jsonl.gz`, `val.jsonl.gz`, `test.jsonl.gz` (Hermes chat template format).

### 5. Train (trainer)

```bash
apprentice-train --dataset-dir <vN> --output-dir <ckpt> --profile <profile>
```

Unsloth QLoRA fine-tuning of the base model. Profile files (`trainer/profiles/`) configure batch size, learning rate, max steps per GPU.

**Multi-base-model:** The base model is resolved from `trainer/supported_models.yaml`. Use `--list-models` to see available options. Default: Qwen2.5-1.5B-Instruct.

### 6. Merge (model_exporter)

```bash
apprentice-merge --base-model <model> --adapter-dir <ckpt>/lora-adapter --output-dir <merged>
```

Merges the LoRA adapter weights into the base model, producing a standalone fp16 model. Writes `training_manifest.json` with Ed25519 signature.

### 7. Baseline (validator)

```bash
apprentice-baseline --test-dataset <test.jsonl.gz> --output <baseline.jsonl>
```

Runs the bare base model on the test set using vLLM offline batch inference. Produces a JSONL file of `(expected, actual)` pairs with counts and metadata.

### 8. Validate (validator)

```bash
apprentice-validate --model-dir <merged> --test-dataset <test.jsonl.gz> \
    --pattern-id <id> --baseline-pairs <baseline.jsonl>
```

Runs the fine-tuned specialist on the test set. Computes exact-match and whitespace-token F1. Compares against baseline scores.

**Promotion gate:**
- `specialist_f1 >= baseline_f1 + 0.10`
- `specialist_exact_match >= baseline_exact_match + 0.10`
- If teacher score provided: `specialist_f1 >= teacher_f1 - 0.05`

All three must pass. On failure, writes a structured failure report to `~/.apprentice/failures/`.

### 9. Promote (registry)

On pass, the validator copies the merged model to `~/.apprentice/registry/<id>/v<N>/`, signs a `registry_manifest.json`, and renders a Hermes SKILL.md that gets scp'd to the microVM.

### 10. Serve

```bash
apprentice-serve --model-dir ~/.apprentice/registry/<id>/latest/ --port 8000
```

vLLM HTTP server serving the specialist. Multi-LoRA mode (`--enable-lora`) hot-swaps adapters on one warm base model via the residency control plane on port 8071.

```bash
# Proxy routes matching requests to the specialist
proxy serve --listen :8083 --serve-url http://localhost:8000 \
    --residency-url http://localhost:8071 \
    --upstream-url https://openrouter.ai/api/v1
```

## Cron-driven automation

The orchestrator watcher is designed to run as a cron job:

```cron
* * * * * apprentice-telegram dispatch-one
* * * * * apprentice-telegram poll-replies
*/2 * * * * apprentice-orchestrator tick
*/10 * * * * apprentice-orchestrator safety advance --all
```

- **dispatch-one** — Delivers one outbound message per tick.
- **poll-replies** — Checks Telegram for operator replies (`train`, `skip`, `budget increase`).
- **tick** — Processes decision markers, runs training pipeline (max 1 GPU job per tick).
- **safety advance --all** — Evaluates canary shadow data and advances/demotes patterns.

## Pattern merging

Two specialists can be combined into one:

```bash
dataset-builder merge --pattern-a <id1> --pattern-b <id2> --output-dir <merged-ds>
apprentice-validate --model-dir <merged-model> --merge-regression <id1> <id2> ...
```

See [Pattern Merging](Pattern-Merging) for details.
