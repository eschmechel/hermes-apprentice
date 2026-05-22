# Benchmark Reference

## Training (Qwen2.5-1.5B-Instruct, QLoRA)

| GPU | VRAM | Batch | Grad Accum | Max Steps | Duration | Cost |
|-----|------|-------|------------|-----------|----------|------|
| A100 80GB | 80 GB | 16 | 1 | 200 | ~45 min | ~$0.90/hr |
| 2080 Ti | 11 GB | 2 | 4 | 60 | ~25 min | local |
| 4060 Mobile | 6-8 GB | 1 | 8 | 30 | ~15 min | local |

Profile files: `trainer/profiles/profile_*.yaml`

## Inference (Qwen2.5-1.5B-Instruct fp16, vLLM)

| Metric | Value |
|--------|-------|
| Model size | ~3 GB fp16 |
| LoRA adapter | ~18 MB |
| p50 latency | ~38ms |
| p95 latency | ~85ms |
| p99 latency | ~150ms |
| Tokens/sec | ~120 t/s (2080 Ti) |
| Max concurrent LoRAs | 4 (default `--max-loras`) |

## Proxy routing

| Metric | Value |
|--------|-------|
| Embedding latency | ~2ms (BGE-small, ONNX) |
| Cosine match (100 patterns) | <0.1ms |
| Pattern centroid size | 384 float32 = 1.5 KB |
| Shadow sampling rate | 5% default |

## Dataset building

| Step | ~100 records | ~1000 records |
|------|-------------|--------------|
| Fetch from observer | <1s | ~2s |
| PII redaction (Presidio) | ~5s | ~30s |
| Quality filter | <0.1s | <0.5s |
| Fuzzy dedup | <0.5s | ~5s |
| Teacher augmentation | ~10s | ~60s |
| Split 80/10/10 | <0.1s | <0.5s |

## Memory (host, idle)

| Component | RAM |
|-----------|-----|
| Observer (Go) | ~15 MB |
| Detector (Go) | ~40 MB (BGE-small) |
| Proxy (Go) | ~30 MB (+ ~1.5 KB per pattern) |
| Registry service (Go) | ~10 MB |
| vLLM idling (no LoRA) | ~3.5 GB VRAM |
| vLLM per LoRA loaded | +18 MB VRAM |
| Presidio sidecar (Docker) | ~500 MB |
| Prometheus | ~200 MB |
| Grafana | ~100 MB |
