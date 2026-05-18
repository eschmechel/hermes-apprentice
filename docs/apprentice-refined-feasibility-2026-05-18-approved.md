# Apprentice: Refined Feasibility Plan

## Correction: the actual contest

The active contest is the **Hermes Agent Challenge** on dev.to (May 15–31, $1K prizes, two tracks: Build With Hermes Agent and Write About Hermes Agent). The $25K Creative Hackathon ended ~May 10. This changes the framing but simplifies the entry — no creative/media requirement, just a clean technical build that incorporates Hermes meaningfully.

## One-paragraph pitch (updated)

Hermes Agent accumulates Markdown skills from experience. This project adds a second loop: when the agent has seen a task pattern enough times, it harvests (input, big-model-output) pairs from its own session history, fine-tunes a small model with LoRA via Unsloth (locally on a 2080 Ti or burst to RunPod), validates the adapter, and registers the result as a Hermes skill that routes future similar requests to a cheap local endpoint. Skills today are prompts. This makes some of them weights.

## Architecture (refined)

Six components, one structural change from the original draft.

### 1. Observer (Go)

Tails Hermes' SQLite session DB at `~/.hermes/state.db` (well-documented schema: `sessions` + `messages` tables, FTS5 for search). Extracts `(timestamp, user_input, assistant_output, model_used, token_counts, latency_ms)`. Writes to a local store. Trajectory export (`hermes sessions export`) is an alternative data source if the raw DB proves fragile across Hermes versions — pin to v0.13.0.

**2080 Ti impact**: none, this is a CPU-side service.

### 2. Pattern Detector (Go)

Runs every 15 minutes via a Hermes cron job:
- `hermes cron create "every 15m" --no-agent --script ~/.hermes/scripts/apprentice-detect.sh --deliver telegram`
- The shell wrapper calls the Go binary
- Embeds recent inputs with a small model (BGE-small via a local ONNX runtime or text-embedding-3-small via OpenRouter)
- Runs HDBSCAN clustering
- When a cluster crosses threshold (20+ inputs, 7-day window, cosine ≥ 0.78), emits a candidate
- Generates a pattern description via the big model from a cluster sample

**Cron + Go**: shell wrapper required. `no_agent=True` means no LLM cost per tick.

### 3. Dataset Builder (Python)

Pulls pairs from the Observer store, deduplicates, runs PII redaction via Microsoft Presidio, quality-filters (only pairs not corrected within 3 turns). Augments via teacher paraphrasing if below quality thresholds. Splits 80/10/10. Writes versioned Parquet.

### 4. Trainer (Python + Unsloth)

Two modes:

| Mode | Hardware | Config | ETA for 200-500 pairs |
|------|----------|--------|-----------------------|
| **Local** | RTX 2080 Ti 11GB | QLoRA 4-bit, rank 16, batch size 2-4 | 10-30 min |
| **Cloud burst** | RunPod A100 80GB spot (~$1.19/hr) | LoRA 16-bit, batch size 16-32 | 2-7 min |

The local 2080 Ti is viable for training: Qwen2.5-1.5B in 4-bit QLoRA uses ~3-4GB VRAM, leaving ~7GB for activations. Batch size will be limited but training still completes. The cloud burst path is for speed and for when larger datasets (>500 pairs) or larger base models (3B+) are needed.

Default hyperparams: rank 16, alpha 32, dropout 0.05, target all linear projections, lr 2e-4 cosine, 3 epochs.

**Tokenizer drift mitigation**: Use `model.save_pretrained_merged("path", tokenizer, save_method="merged_16bit")` — merges LoRA into base in fp16. The output is a standard model that vLLM loads without adapter machinery. This avoids the lm_head shape mismatch entirely, at the cost of losing hot-swap capability. Hot-swap via adapter-only loading becomes a v0.2 optimization once the tokenizer mismatch edge cases are handled.

### 5. Validator (Go + Python)

Spins vLLM with merged model, runs held-out test split through both the specialist and a few-shot baseline on the raw base model. Promotion gate:
- Adapter beats few-shot baseline on the small base model by ≥10 points
- Adapter is within 5 points of the big model response quality (exact-match or domain-appropriate metric)

On success: signs the manifest with Ed25519, writes the model to `~/apprentice/registry/`.

### 6. Proxy + Skill (two-layer router)

The router is a **combo approach**:

**Layer 1 — Go proxy server** (deterministic routing):
A lightweight Go HTTP server that serves an OpenAI-compatible `/v1/chat/completions` endpoint. Hermes points one profile's model to this proxy. The proxy:
- Maintains a list of pattern → specialist model mappings
- Embeds each incoming user input and compares against pattern centroids
- If match found and confidence above threshold: forwards to local vLLM serving the merged specialist model
- If match uncertain: routes to upstream big model (OpenRouter, etc.)
- If specialist response fails validation: falls back to big model transparently
- Dedicates 5% of matched requests to both specialist and big model for shadow comparison

This gives deterministic routing regardless of how Hermes' skill selector behaves.

**Layer 2 — Hermes skill** (user-visible interface):
A standard SKILL.md registered via `skill_manage` or filesystem write. The skill's `description` is crafted so the LLM is likely to load it for matching requests. The skill body says: "Your model endpoint is already configured to route specialist tasks — just respond normally. The proxy handles fallback." This layer exists so the specialist is visible in Hermes' skill ecosystem (discoverable, removable, refinable) even though the real routing happens at the proxy.

**Edge case**: the Hermes skill selector is LLM-judged (all skill descriptions injected into system prompt, agent decides which to load). This is non-deterministic. The proxy is the reliable path. The skill is documentation + discoverability.

## Model choice (updated)

**Base model**: Qwen2.5-1.5B-Instruct (Apache 2.0)
- Fits the 2080 Ti 11GB for both training (QLoRA) and inference (fp16 merged)
- Qwen2.5 explicitly claims "improved comprehension of structured data and reliable generation of structured outputs, particularly in JSON format" (official release blog, Sept 2024)
- Apache 2.0 license — clean for portfolio

**Strong alternative for v0.2**: Qwen3-1.7B (released April 2025, Apache 2.0)
- Matches or outperforms Qwen2.5-3B on most benchmarks
- Still fits the 2080 Ti (3.5GB in 4-bit)
- Wait until Unsloth's adapter saving bug (Issue #3428) is resolved

## MVP scope (updated for deadline and hardware)

**Day 1 (May 17)** — Install Hermes, prove the skill registration path. Read `skill_manage` source. Write a throwaway skill, register it, confirm it loads.

**Day 2** — Observer + Pattern Detector skeleton in Go. Tail the session DB, print clusters to stdout.

**Day 3** — Dataset Builder + local Trainer. Unsloth on Qwen2.5-1.5B with 25 preloaded email-extraction pairs. Target: training completes in under 20 min on the 2080 Ti. Measure actual runtime.

**Day 4** — vLLM serving + Validator. Serve the merged model on the 2080 Ti. Compute exact-match against held-out set. Confirm 38ms vs 1.4s latency claim is in the right ballpark.

**Day 5** — Go proxy server. OpenAI-compatible endpoint, embedding-based pattern matching, upstream fallback to OpenRouter.

**Day 6** — Hermes skill for the specialist. Wire skill_manage registration from the Validator. End-to-end: Observer → Detector → Builder → Trainer → Validator → Proxy registration.

**Day 7** — Buffer for the one nasty bug (likely tokenizer mismatch or vLLM serving issue on the 2080 Ti).

**Day 8** — Cost/latency instrumentation. Structured JSON logs, Prometheus counters if available.

**Day 9** — Telegram polish. Graduation message, weekly summary, failure message. Tune phrasing.

**Day 10** — Cloud burst dispatcher (stubbed). The Go dispatcher logic for RunPod exists but the demo runs locally.

**Day 11** — Shadow validation in the proxy. 5% sampling, comparison logging, drift detection.

**Day 12** — README, architecture diagram, repo polish.

**Day 13** — Record demo video (3 min). Today is May 17 — deadline is May 31. This gives 4 days of buffer if anything slips.

**Day 14** — Write the dev.to essay.

**Day 15** — Essay revision, cross-post to eschmechel.dev.

**Day 16 (May 31)** — Submit on dev.to. Post on HN, Nous Discord, r/LocalLLaMA.

**Hardware reality**: All local testing and development on the 2080 Ti 11GB. The demo shows local training. Cloud burst exists in code but is documented as "for speed" not "because local doesn't work."

## Cost projections (corrected)

RunPod pricing (Secure Cloud):
| GPU | On-demand | Spot (Community Cloud) |
|-----|-----------|----------------------|
| RTX 4090 24GB | $0.69/hr | ~$0.17/hr |
| A100 80GB | $1.39/hr | ~$0.80/hr |
| H100 80GB | $2.39/hr | ~$1.25/hr |

Realistic per-specialist cost: **~$0.30–$0.80 on cloud burst, $0 on homelab (just time)**.

RouteLLM comparison numbers (corrected from original):
| Benchmark | Cost Savings at CPT(50%) |
|-----------|--------------------------|
| MT-Bench | ~73% (up to 85% at aggressive thresholds) |
| MMLU | ~29% |
| GSM8K | ~33% |

## Competitive landscape (corrected and tightened)

Key additions to the original analysis:

1. **Hermes already has Atropos + Tinker for RL training from trajectories.** This is acknowledged in the Hermes docs as "research-ready." The difference: Atropos trains a general RL model; Apprentice trains per-task specialists triggered by pattern detection and routes to them selectively.

2. **DSPy BootstrapFinetune** can compile prompts into weight updates, but it compiles a *human-defined program*, not a *recurring task pattern detected from history*. The autonomic triggering is the differentiator.

3. **Voyager** comparison stands: code skills vs weight skills. The plan's characterization is accurate.

4. **GEPA** comparison stands: prompt-space vs weight-space. Accurate.

## Key design decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Router mechanism | Go proxy + descriptive skill | Proxy gives deterministic routing; skill gives Hermes ecosystem integration |
| LoRA deployment | Merge to fp16 (`save_pretrained_merged`) | Avoids tokenizer drift; simpler ops; hot-swap can come later |
| Training hardware | Local 2080 Ti (QLoRA 4-bit) + cloud burst (RunPod) | 2080 Ti works for 1.5B; cloud for speed/larger models |
| Cron → Go binary | Shell wrapper script | Required for `no_agent` mode (Hermes passes non-.sh files to Python) |
| Pattern detection | HDBSCAN + centroid embedding | Simple, well-understood, no learned model to maintain |
| Validation metric | Exact match (for JSON extraction) | Simple, unambiguous, domain-appropriate for MVP |
| Inference serving | vLLM on the 2080 Ti | Fits: Qwen2.5-1.5B fp16 = ~3.1GB; plenty of headroom for serving |

## Open questions remaining

1. **Contest submission format**: Must confirm whether the dev.to Challenge requires a public demo URL or if a video + dev.to post is sufficient. Check `dev.to/page/hermes-agent-challenge-2026-05-15` directly.

2. **Hermes version pinning**: The session DB schema is not a public API. Pin to Hermes v0.13.0 (May 7, 2026) for the build. Document migration path for future versions.

3. **Embedding model for pattern detection**: BGE-small via ONNX (local, fast, free) vs text-embedding-3-small via OpenRouter (~$0.0001/query, negligible but adds a dependency). Decision: start with BGE-small ONNX, fall back to OpenRouter if quality is poor.

4. **Proxy model routing granularity**: At what level does the proxy match patterns? Per-user, per-profile, or global? For MVP, global is simplest — one user, one agent, one proxy.

5. **2080 Ti inference latency**: The plan estimates 38ms median on a 4090. The 2080 Ti has ~60% of the CUDA cores and slower memory bandwidth. Expect ~60-80ms median. Still orders of magnitude faster than an API call, but worth measuring on day 4 before committing to latency numbers for the demo.


---

# Plan Feedback

I've reviewed this plan and have 4 pieces of feedback:

## 1. (line 172) Feedback on: "Contest submission format: Must confirm whether the dev.to Challenge requires a public demo URL or if a video + dev.to post is sufficient. Check dev.to/page/hermes-agent-challenge-2026-05-15 directly."
> This is the submission format:
*This is a submission for the [Hermes Agent Challenge](https://dev.to/challenges/hermes-agent-2026-05-15)*

## What I Built
<!-- Provide an overview of your project and what problem it solves or experience it creates. -->

## Demo
<!-- Embed a video walkthrough or share screenshots of your deployed project. -->

## Code
<!-- Embed or share a link to your repository. -->

### My Tech Stack
<!-- Share your tech stack. -->

## How I Used Hermes Agent
<!-- Explain how Hermes Agent powers your project. Tell us what agentic capabilities you leaned on and why they were the right fit for your use case. -->

<!-- Team Submissions: Please pick one member to publish the submission and credit teammates by listing their DEV usernames directly in the body of the post. -->

<!-- Thanks for participating! -->

## 2. (line 176) Feedback on: "Embedding model for pattern detection: BGE-small via ONNX (local, fast, free) vs text-embedding-3-small via OpenRouter (~$0.0001/query, negligible but adds a dependency). Decision: start with BGE-small ONNX, fall back to OpenRouter if quality is poor."
> Sure

## 3. (line 178) Feedback on: "Proxy model routing granularity: At what level does the proxy match patterns? Per-user, per-profile, or global? For MVP, global is simplest — one user, one agent, one proxy."
> Global say with intentions in future verions for per user/agent

## 4. (line 180) Feedback on: "2080 Ti inference latency: The plan estimates 38ms median on a 4090. The 2080 Ti has ~60% of the CUDA cores and slower memory bandwidth. Expect ~60-80ms median. Still orders of magnitude faster than an API call, but worth measuring on day 4 before committing to latency numbers for the demo."
> For all purposes let's plan for us to use cloud throughout development. But to also incorporate the ability to use local. We test local only to prove it's possible

---
