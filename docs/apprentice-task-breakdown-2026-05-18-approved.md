# Apprentice — Task Breakdown

## Structure

11 features, ~40 atomic subtasks. Each feature maps to one or more days from the 16-day plan. Dependencies between features define the build order. Within a feature, subtasks may be sequential or parallel.

### Feature Dependency Graph

```
foundation ──► observer ──► detector ──► dataset-builder ──► trainer ──► validator ──► proxy ──► integration
                   │                                                         │            │
                   │                                                         ▼            │
                   └──► (detector output feeds dataset-builder)         serving ◄────────┘
                                                                            │
                                                                            ▼
                                                                        skill
                                                                           │
                                                                           ▼
                                                                     telegram ──► demo

instrumentation runs alongside everything from day 8+
```

### Legend

- `[seq]` — sequential: must run after prior task in this feature
- `[parallel]` — can run alongside other parallel tasks with satisfied deps
- Dependencies refer to other features by name + subtask number, e.g., `observer-04` means "observer subtask 04"
- `acceptance` = what must be true to call it done
- `deliverables` = files produced

---

## Feature: foundation

**Objective**: Prove the Hermes integration surface works — skill registration, session DB access, cron ticks.
**Exit criteria**: A throwaway skill is registered and invoked; session DB is readable; cron tick fires a shell script.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Install Hermes v0.13.0, verify CLI works | — | — | CoderAgent | `hermes --version` returns v0.13.0 |
| 02 | Read `skill_manage` source, understand create/patch/delete flow | 01 | — | explore | Document the six actions and payload format |
| 03 | Read Hermes session DB schema from `hermes_state.py` or docs | 01 | — | explore | Document `sessions` and `messages` table schemas |
| 04 | Read cron tick implementation | 01 | — | explore | Document tick flow, `no_agent` mode, script resolution |
| 05 | Read Hermes profiles implementation | 01 | — | explore | Document profile isolation model |
| 06 | Write throwaway skill that calls `terminal` tool with a simple script | 02 | — | CoderAgent | Skill registered via `skill_manage`, agent invokes it and returns output |
| 07 | Write a `no_agent` cron job that runs a shell wrapper and delivers to Telegram | 04 | — | CoderAgent | Cron fires every N minutes, output appears in Telegram |

---

## Feature: observer

**Objective**: Go service that tails the Hermes session DB and extracts structured (input, output, metadata) pairs.
**Exit criteria**: Observer starts, detects new messages in the DB, writes normalized records to its own store.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Go project scaffold: `go mod init`, cobra CLI, SQLite driver, basic HTTP server | foundation-03 | — | CoderAgent | Binary compiles, `--help` works |
| 02 | Implement session DB polling: read `messages` table on timer, extract `(session_id, role, content, timestamp, tool_calls)` | 01 | — | CoderAgent | Service logs new messages as they appear in Hermes DB |
| 03 | Deduplicate and normalize records: same content + same session within 60s = skip | 02 | — | CoderAgent | Idempotent on restart |
| 04 | Write normalized records to local SQLite store with schema: `(pattern_id, input_hash, input_text, output_text, system_prompt_hash, model_used, latency_ms, token_counts)` | 03 | — | CoderAgent | Records survive restart; queryable by pattern_id |
| 05 | Expose HTTP endpoint `GET /records?since=<timestamp>&pattern=<pattern_id>` for downstream consumers | 04 | — | CoderAgent | `curl` returns JSON array of records |

---

## Feature: detector

**Objective**: Go service that clusters recent inputs and emits candidate task patterns when clusters cross threshold.
**Exit criteria**: With 25 seeded email-extraction inputs, detector emits one candidate pattern above threshold.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Integrate BGE-small embedding model via ONNX runtime | observer-05 | — | CoderAgent | `embed("extract name from this email")` returns a float vector |
| 02 | Implement input hashing and lookup: hash new inputs, skip if seen within 24h | observer-05 | 01 | CoderAgent | No duplicate embeddings for repeated content |
| 03 | Implement HDBSCAN clustering over recent (7-day) embeddings | 01, 02 | — | CoderAgent | With 25 similar inputs, cluster of ≥20 is emitted |
| 04 | When cluster crosses threshold (≥20 inputs, cosine ≥ 0.78), generate pattern description via OpenRouter call | 03 | — | CoderAgent | Description like "Extract structured fields from customer emails" |
| 05 | Store candidate as `~/apprentice/patterns/<id>/manifest.json` with centroid embedding, description, record count | 04 | — | CoderAgent | Pattern survives restart |
| 06 | Expose HTTP endpoint `GET /patterns` and `POST /patterns/:id/approve` | 05 | — | CoderAgent | `curl` returns patterns list |

---

## Feature: dataset-builder

**Objective**: Python worker that converts a pattern's records into a training-ready dataset.
**Exit criteria**: Given a pattern ID, produces train/val/test JSONL with PII redacted and quality-filtered.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Python project scaffold: `uv init`, dependencies (unsloth, presidio, tokenizers, huggingface-hub) | — | — | CoderAgent | `uv run python -c "import unsloth"` succeeds |
| 02 | Implement record fetcher: pull records from Observer API for a pattern ID | detector-06 | — | CoderAgent | Returns list of (input, output) pairs |
| 03 | Implement PII redaction pass via Presidio: names, emails, phones, addresses → placeholder tokens | 01 | — | CoderAgent | "john@email.com" becomes "[EMAIL_1]" |
| 04 | Implement quality filter: drop pairs where user re-asked or corrected within 3 turns | 02 | — | CoderAgent | Only "accepted" outputs survive |
| 05 | Implement deduplication: fuzzy dedup (edit distance > 0.85) on input text | 03, 04 | — | CoderAgent | Near-duplicate inputs produce one training row |
| 06 | Implement teacher augmentation: for datasets < 200 pairs, use big model to paraphrase inputs with preserved output schema | 05 | — | CoderAgent | 25 seed pairs become ~200 after augmentation |
| 07 | Split 80/10/10, serialize train/val/test JSONL with Hermes chat template format | 06 | — | CoderAgent | Files are valid JSONL with role/content structure |
| 08 | Save versioned dataset to `~/apprentice/datasets/<pattern-id>/v<n>/`, write manifest with SHA-256 | 07 | — | CoderAgent | Manifest has `dataset_hash`, `row_count`, `augmentation_count` |

---

## Feature: trainer

**Objective**: Python worker that takes a dataset and runs Unsloth fine-tuning, producing a merged model.
**Exit criteria**: Given a dataset of ~200 pairs on Qwen2.5-1.5B, produces a merged fp16 model in < 30 min.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Implement Unsloth training script: load Qwen2.5-1.5B-Instruct 4-bit, LoRA rank 16, train on JSONL | dataset-builder-08 | — | CoderAgent | Training runs without error on 25 test pairs |
| 02 | Add hyperparameter profile for 2080 Ti: batch size 2-4, gradient accumulation 2-4, QLoRA only | 01 | — | CoderAgent | Training completes without OOM on 11GB |
| 03 | Add hyperparameter profile for RunPod A100: batch size 16-32, LoRA 16-bit, no quantization | 01 | — | CoderAgent | Training completes in < 10 min |
| 04 | Implement merged model output: `model.save_pretrained_merged("path", tokenizer, save_method="merged_16bit")` | 01 | — | CoderAgent | Output directory contains `config.json`, `model.safetensors`, `tokenizer.json` |
| 05 | Implement cloud burst dispatcher: Go side that provisions RunPod pod via runpodctl, rsyncs dataset, triggers training, rsyncs model back | 03 | — | CoderAgent | `./apprentice train --mode=burst` provisions pod and returns merged model path |
| 06 | Write `training_manifest.json` with dataset hash, base model, hyperparams, runtime, exit code | 04, 05 | — | CoderAgent | Manifest is valid JSON |
| 07 | Sign manifest with Ed25519 key (generated on first run, stored at `~/.apprentice/keys/`) | 06 | — | CoderAgent | `manifest.sig` verifies against `manifest.json` |

---

## Feature: serving

**Objective**: vLLM serving layer that hosts the specialist model.
**Exit criteria**: vLLM serves a merged model, responds to `/v1/chat/completions` with correct output.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Install vLLM, stand up server with merged Qwen2.5-1.5B: `vllm serve path --port 8000` | trainer-04 | — | CoderAgent | `curl localhost:8000/v1/chat/completions` returns valid response |
| 02 | Confirm API shape matches OpenAI spec (Hermes compatibility) | 01 | — | CoderAgent | Hermes profile pointing at this endpoint responds correctly |
| 03 | Measure and document inference latency on the 2080 Ti for merged 1.5B model | 01 | — | CoderAgent | p50 latency documented (estimate: 60-80ms) |
| 04 | Write systemd unit for auto-start on boot | 01 | — | CoderAgent | `systemctl --user enable apprentice-vllm` works |

---

## Feature: validator

**Objective**: Gate that determines whether a trained specialist is good enough to promote.
**Exit criteria**: Given a merged model and held-out test set, produces a pass/fail verdict with metrics.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Implement test runner: load test JSONL, run each input through vLLM specialist endpoint, collect outputs | serving-01, dataset-builder-08 | — | CoderAgent | Returns list of (expected, actual) pairs |
| 02 | Implement baseline runner: same test set through few-shot prompt on raw Qwen2.5-1.5B (via API or local) | 01 | — | CoderAgent | Returns comparable (expected, actual) pairs |
| 03 | Compute exact-match and F1 scores for specialist and baseline | 01, 02 | — | CoderAgent | Scores are numeric, comparable |
| 04 | Implement promotion gate: specialist ≥ baseline + 10 AND specialist ≥ teacher - 5 | 03 | — | CoderAgent | Boolean pass/fail with per-metric breakdown |
| 05 | On pass: copy merged model to `~/apprentice/registry/<skill-id>/v<n>/`, sign manifest | 04 | — | CoderAgent | Registry has model files + signed manifest |
| 06 | On fail: write failure report to `~/apprentice/failures/<run-id>.json`, notify user | 04 | — | CoderAgent | Report contains scores, dataset info, suggested action |
| 07 | Expose HTTP endpoint for the proxy to query validation results | 06 | — | CoderAgent | `GET /registry/<skill-id>/latest` returns model path + scores |

---

## Feature: proxy

**Objective**: Go HTTP proxy that sits between Hermes and its model endpoint, routing similar requests to the specialist.
**Exit criteria**: Proxy forwards requests; matching inputs go to specialist; fallback works; 5% shadow sampling works.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Go HTTP server with OpenAI-compatible `/v1/chat/completions` endpoint | observer-01 | — | CoderAgent | Proxy responds with same shape as OpenAI API |
| 02 | Implement upstream pass-through: non-matching requests forwarded to OpenRouter (or configured provider) | 01 | — | CoderAgent | Request reaches OpenRouter, response returns |
| 03 | Implement pattern matching: embed incoming input via BGE-small, cosine against registered pattern centroids | 01, detector-06 | — | CoderAgent | Inputs above threshold cosine match to pattern |
| 04 | Implement specialist route: matching requests forwarded to local vLLM specialist server | 03, validator-07 | — | CoderAgent | Matching request returns specialist response |
| 05 | Implement fallback: if specialist response invalid (schema mismatch) → retry with upstream | 04 | — | CoderAgent | Corrupt specialist response transparently falls back |
| 06 | Implement 5% shadow sampling: randomly sample matching requests, send to BOTH specialist and upstream, log comparison | 04 | — | CoderAgent | Log file has (input, specialist_output, upstream_output, latency_both) |
| 07 | Implement pattern registration endpoint: `POST /patterns` adds a new centroid + specialist model | 03 | — | CoderAgent | Proxy hot-reloads patterns without restart |
| 08 | Configure Hermes profile to point at proxy as model provider | 01 | — | CoderAgent | `hermes model` set to proxy URL; chat works |

---

## Feature: skill

**Objective**: Hermes SKILL.md that describes the specialist, making it visible in Hermes' skill ecosystem.
**Exit criteria**: Skill appears in `skills_list`, agent loads it for matching requests.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Write SKILL.md with name, description, category, and instructions | — | — | CoderAgent | Valid YAML frontmatter, no missing fields |
| 02 | Craft trigger description so LLM is likely to match for specialist-worthy requests | 01 | — | CoderAgent | With test input "extract fields from: ...", LLM picks this skill |
| 03 | Skill body instructs the agent that routing is handled by the proxy — respond normally | 02 | — | CoderAgent | Agent loads skill and responds as expected |
| 04 | Register skill automatically from Validator: copy SKILL.md to `~/.hermes/skills/<id>/` | validator-05 | — | CoderAgent | After promotion, skill appears in `~/.hermes/skills/` |
| 05 | Implement `/reload-skills` call or next-session pickup | 04 | — | CoderAgent | Hermes detects new skill without manual intervention |

---

## Feature: instrumentation

**Objective**: Cost/latency tracking for the demo graphs.
**Exit criteria**: Prometheus counters or structured JSON logs produce data for latency and cost-over-time graphs.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Add latency logging to proxy: per-request p50/p99 for specialist vs upstream | proxy-04 | — | CoderAgent | Log line has `{method, latency_ms, model, pattern_id}` |
| 02 | Add cost tracking: compute per-request cost from OpenRouter token pricing | proxy-04 | — | CoderAgent | Log line has `{estimated_cost_usd}` |
| 03 | Write weekly summary generator: read 7-day logs, produce (volume, avg_latency, total_cost, cost_saved) per pattern | 01, 02 | — | CoderAgent | Summary is valid JSON |
| 04 | Emit Prometheus counters if prometheus client library is available | 01, 02 | — | CoderAgent | `/metrics` endpoint exposes relevant counters |

---

## Feature: telegram

**Objective**: User-facing notifications for graduation, weekly summary, and failures.
**Exit criteria**: Hermes posts graduation message, weekly summary, and failure report to Telegram.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Write graduation message template: "I've handled N similar requests... Reply `train` to proceed, `details` to see the dataset, `skip`." | detector-06 | — | CoderAgent | Message is clear, actionable |
| 02 | Write failure message template: "Training for PATTERN failed — specialist only matched BIG_MODEL on SCORE%. Won't deploy." | validator-06 | — | CoderAgent | Message is honest, provides scores |
| 03 | Write weekly summary template: "This week: N requests, N served locally, N escalated. Total saved: $X and Y seconds. Next candidate pattern: PATTERN (Z examples)." | instrumentation-03 | — | CoderAgent | Message has correct numbers |
| 04 | Wire templates into Hermes cron-aware Telegram delivery | 01, 02, 03 | — | CoderAgent | Messages appear in configured Telegram channel |
| 05 | Wire `train`/`details`/`skip` reply handler: user reply triggers training or detail dump | 04 | — | CoderAgent | Reply `details` returns dataset preview |
| 06 | Tune message phrasing for natural tone | 04, 05 | — | CoderAgent | Self-review: no marketing-speak, no filler |

---

## Feature: demo

**Objective**: Contest submission — video, essay, repo, README.
**Exit criteria**: Submitted to dev.to by May 31. Repo has README with GIF.

### Tasks

| # | Title | Deps | Parallel | Suggested agent | Acceptance criteria |
|---|-------|------|----------|-----------------|---------------------|
| 01 | Write README.md with architecture diagram, quickstart, GIF of graduation moment | everything above | — | DocWriter | README is complete, recruiter-visible |
| 02 | Record demo video (3 min): Telegram interactions → graduation moment → latency graph | integration (e2e must work) | — | CoderAgent | 3 min, clean audio, side-by-side latency graph |
| 03 | Write dev.to essay (~2500 words): cold open → Hermes background → skill-as-weights → architecture → numbers → failures → close | 01, 02 | — | DocWriter | Fits the Write About Hermes Agent track |
| 04 | Set up eschmechel.dev mirror, configure canonical URL back to dev.to | 03 | — | CoderAgent | Canonical tag points to dev.to |
| 05 | Cross-post to lobste.rs, HN, Nous Discord, r/LocalLLaMA | 03 | 04 | — | Links posted, engagement monitored |
| 06 | Submit to dev.to with correct tags and contest form | 03 | — | — | dev.to post has `hermes-agent-challenge-2026-05-15` tag |

---

## Execution order summary

### Phase 1 (Days 1-3): Foundation + Core Data Pipeline
```
foundation (all 7) → observer (5) → detector (6)
```

### Phase 2 (Days 4-6): Training + Serving
```
detector-06 → dataset-builder (8) → trainer (7)
                                            ↓
                                     serving (4)
```

### Phase 3 (Days 7-9): Validation + Routing
```
trainer-07 → validator (7) → proxy (8)
serving-04 → validator-01 → proxy-04
                             ↓
                          skill (5)
```

### Phase 4 (Days 10-12): Polish + Instrumentation
```
proxy-08 → integration testing
instrumentation (4)
telegram (6)
```

### Phase 5 (Days 13-16): Demo + Submission
```
demo (6) — includes README, video, essay, cross-posts
```

---

## Risk checkpoints

| Day | Check | Action if failed |
|-----|-------|------------------|
| 1 | `skill_manage` create works from filesystem path | Fall back to agent-mediated registration via queue |
| 3 | Observer reads Hermes DB without schema errors | Pin Hermes version, use trajectory export as fallback |
| 6 | Training completes in < 30 min on 2080 Ti | Cut to cloud-only training for MVP, skip local |
| 9 | Proxy forwards specialist requests correctly | Fall back to direct skill-based routing (no proxy) |
| 12 | End-to-end green on seeded data | Cut shadow validation, cut cloud burst stub, record demo with known output |
