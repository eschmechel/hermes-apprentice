# Known Gaps & Roadmap

## v0.2 known gaps

| Gap | Impact | Mitigation |
|-----|--------|------------|
| **No true multimodal** | Text-only specialists. Image/audio training deferred. | Multi-base-model for text. Multimodal planned for v0.3. |
| **Single GPU serialization** | One training job per orchestrator tick. | `max_jobs=1` default. Increase if multi-GPU available. |
| **Firecracker-only Hermes** | Docker profile is a fallback, not the default path. | Docker Compose available but less tested. |
| **OpenRouter live verification pending** | E2E tests against mocks. Real API key testing deferred. | See `notes/openrouter-live-verification.md`. |
| **Python 3.14 untested in CI** | pyproject constraints relaxed but no automated 3.14 testing. | Works locally. CI matrix may need expansion. |
| **Embedder at 62.6% coverage** | Requires ONNX runtime for full coverage. | Core tokenizer tests cover the logic path. ONNX-dependent code tested via integration. |
| **RunPod burst error recovery** | No automatic retry on spot preemption. | Training restarts on next tick. Pod cleanup is manual. |
| **Pattern merging requires operator** | Cannot auto-merge patterns. | By design — merging changes routing behavior and needs human judgment. |

## v0.3 roadmap

### High priority

- **True multimodal specialists** — Image, audio, and vision model support.
- **Multi-GPU scheduling** — Parallel training across multiple GPUs with resource allocation.
- **Streaming response support** — SSE streaming in the proxy for upstream specialists.
- **Model fine-tuning history** — Track all training runs, versions, and scores across a specialist's lifecycle.
- **Automated canary recovery** — Auto-retrain on demotion with updated dataset.

### Medium priority

- **Docker-native Hermes** — First-class Docker profile as alternative to Firecracker.
- **Dataset quality scoring** — Automatic quality scoring before training (diversity, coverage, noise).
- **Pattern auto-evolution** — Detect drift in specialist response quality and trigger re-training.
- **Web dashboard** — Standalone management UI (beyond the Vue.js SPA on `/dashboard`).
- **Slack/Discord notification channels** — Extend Telegram-only notification to other platforms.

### Low priority

- **Model registry web UI** — Browse promoted specialists, compare versions.
- **Dataset explorer** — View training data before committing to training.
- **Cost forecasting** — Predict monthly spend based on historical usage patterns.
- **A/B testing framework** — Compare specialist versions in production with statistical significance.

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) in the repo root for code conventions, test requirements, and commit format.
