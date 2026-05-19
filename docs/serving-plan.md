# Serving Milestone — Implementation Plan

## Context

| Item | Detail |
|------|--------|
| Target hardware | 2080 Ti 11GB (this dev machine: 4060 Laptop 8GB — proof/testing only) |
| Model size | Qwen2.5-1.5B fp16 ≈ 3.1GB VRAM |
| vLLM status | Not installed (will be installed as part of trainer/serving setup on GPU host) |
| Registry | Go `registry-service` already built (port 8082, `GET /registry/{skill-id}/latest`) |
| Merged models | None yet — serving must work with `--model-dir` (direct path) for testing now, and `--pattern-id` (registry lookup) for production later |
| Expected latency | 60-80ms median on 2080 Ti (plan estimate: 38ms on 4090 × ~2x for 2080 Ti) |

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         serving package                         │
│                                                                 │
│  apprentice-serve CLI                                           │
│  ├── --pattern-id <id>  →  GET /registry/{id}/latest (Go svc)  │
│  │                         → resolve model path                 │
│  ├── --model-dir <path>  →  direct model path                  │
│  │                                                              │
│  └── exec: vllm serve <model-path> --host 0.0.0.0 --port 8000  │
│                                                                 │
│  apprentice-bench CLI                                           │
│  └── POST /v1/chat/completions × N requests                     │
│      → measure wall-clock time per request                      │
│      → output p50/p95/p99 JSON                                  │
│                                                                 │
│  systemd unit: apprentice-vllm@.service                         │
│  └── ExecStart=apprentice-serve --pattern-id %i                 │
└─────────────────────────────────────────────────────────────────┘

Go registry-service (port 8082)          vLLM server (port 8000)
┌──────────────────────────┐            ┌─────────────────────────┐
│ GET /registry/x/latest   │            │ /v1/chat/completions    │
│ → { version, manifest }  │──────────► │ serves merged fp16      │
└──────────────────────────┘            └─────────────────────────┘
```

## Subtask 01: vLLM server launcher

**package**: `serving/src/apprentice_serving/server.py` + `cli.py`

### Two modes
1. `--model-dir <path>` — direct path; skip registry lookup
2. `--pattern-id <id>` — query Go registry-service, extract `model_dir` from manifest, then launch

### Registry lookup flow
```
GET http://localhost:8082/registry/{pattern-id}/latest
→ { "found": true, "version": N, "manifest": { "model_dir": "/home/...", ... } }
→ extract model_dir from manifest JSON
→ validate model_dir exists + has config.json
→ exec vllm serve
```

### vLLM launch approach
**Decision**: shell out to `vllm serve` (subprocess, not programmatic import).  
**Rationale**: 
- The validator already demonstrates vLLM offline API works via deferred import
- Server mode (`vllm serve`) is a standalone process with its own async loop, signal handlers, and CLI parsing
- Wrapping it cleanly as a subprocess avoids fighting its event loop
- Pass-through stdout/stderr + signal forwarding (SIGINT/SIGTERM → subprocess)

### `apprentice-serve` CLI args
```
apprentice-serve
  --pattern-id <id>         registry lookup (mutually exclusive with --model-dir)
  --model-dir <path>        direct model path
  --port 8000               vLLM listen port
  --host 0.0.0.0            vLLM bind address
  --registry-url http://localhost:8082   Go registry endpoint
  --gpu-memory-util 0.90    vLLM GPU memory fraction
  --max-model-len 2048      vLLM max context length
  --check-only              validate args + registry + model path, don't launch
  -v, --verbose
```

### `--check-only` flow (CPU-safe, no vLLM import)
1. If `--pattern-id`: query registry, validate response, check model_dir exists
2. If `--model-dir`: validate path exists, has config.json
3. Print resolved config and exit 0

### Files
| File | Purpose |
|------|---------|
| `server.py` | `resolve_model_path()`, `build_vllm_cmd()`, `launch_server()` |
| `cli.py` | argparse, `--check-only`, `main()` |
| `logging.py` | _JSONFormatter + setup_logging (reuse pattern) |

### Python dependency
- `httpx` for registry HTTP lookup (already in pyproject.toml draft)
- No vLLM import — everything is subprocess

## Subtask 02: OpenAI API compatibility verification

**package**: `serving/src/apprentice_serving/compat.py` (or `tests/test_compat.py`)

### What to verify
vLLM's `/v1/chat/completions` is OpenAI-compatible by design, but we need to confirm:
1. Accepts `{"model": "...", "messages": [{role, content}]}` 
2. Returns `{"choices": [{"message": {"role": "assistant", "content": "..."}, "finish_reason": "stop"}], "usage": {...}}`
3. Streaming works: `stream: true` sends SSE chunks
4. Error handling: malformed requests get proper HTTP 4xx + JSON error body

### Approach
- `apprentice-bench` (see subtask 03) doubles as the compatibility test
- A dedicated `test_compat.py` sends 3-4 requests (valid, invalid model, empty messages, stream)
- Runs against a live vLLM endpoint (requires GPU + running server)
- Marked `@pytest.mark.gpu` so it's skipped by default on CPU

### Acceptance
- Hermes can be pointed at `http://localhost:8000/v1` and respond correctly
- Response JSON shape matches the [OpenAI chat completions spec](https://platform.openai.com/docs/api-reference/chat)

## Subtask 03: Latency benchmark

**package**: `serving/src/apprentice_serving/bench.py`

### Design
```
apprentice-bench
  --endpoint http://localhost:8000/v1/chat/completions
  --dataset /path/to/test.jsonl.gz     (or --prompts from stdin)
  --requests 100                        number of requests
  --concurrency 1                       concurrent requests (default sequential)
  --max-tokens 256
  --warmup 5                            warmup requests (excluded from stats)
  --json                                 output as JSON (default: human table)
```

### Measurement
- Time each request: start = just before `httpx.post()`, end = full response received
- Track: total latency, time-to-first-token (if streaming), token count
- Compute: p50, p95, p99, mean, min, max
- Output JSON matching `{p50_ms, p95_ms, p99_ms, mean_ms, min_ms, max_ms, requests, errors}`

### Prompts
- Load from test.jsonl.gz (same format as validator — gzipped JSONL with `{messages: [...]}`)
- Extract the last user message as the prompt
- Send as `{"model": "apprentice", "messages": [{"role": "user", "content": "..."}]}`

### Dependency
- `httpx` (already in pyproject.toml)
- No vLLM import — pure HTTP client

## Subtask 04: systemd user unit

**file**: `serving/contrib/apprentice-vllm@.service`

### Template unit
```ini
[Unit]
Description=Apprentice vLLM serving for pattern %i
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/apprentice-serve --pattern-id %i
Restart=on-failure
RestartSec=10
Environment="PATH=/usr/local/bin:/usr/bin:/bin"

[Install]
WantedBy=default.target
```

### Usage
```bash
# Install
install -D -m644 contrib/apprentice-vllm@.service ~/.config/systemd/user/
systemctl --user daemon-reload

# Enable for a pattern
systemctl --user enable apprentice-vllm@email-extraction.service
systemctl --user start apprentice-vllm@email-extraction.service
```

### What it does
- Template unit: `%i` = pattern-id (instance name)
- Calls `apprentice-serve --pattern-id <id>` which resolves the model path from the registry
- Restarts on failure with 10s delay
- Starts after network is online (needed for registry lookup)

## File listing

```
serving/
├── pyproject.toml              # hatchling, console_scripts, pytest config
├── contrib/
│   └── apprentice-vllm@.service  # systemd user template unit
├── src/apprentice_serving/
│   ├── __init__.py
│   ├── logging.py               # _JSONFormatter + setup_logging (mirrors validator)
│   ├── server.py                # resolve_model_path, build_vllm_cmd, launch_server
│   ├── bench.py                 # latency benchmark: send requests, compute p50/p95/p99
│   └── cli.py                   # apprentice-serve + apprentice-bench entry points
└── tests/
    ├── __init__.py
    ├── test_server.py           # --check-only path, registry mock, model validation
    └── test_bench.py            # latency stats computation (no live endpoint needed)
```

## Test strategy (CPU-only, no GPU or vLLM needed)

| Test | What it validates |
|------|-------------------|
| `test_cli_help` | `--help` prints without imports |
| `test_check_only_model_dir_valid` | Valid model-dir → exit 0 |
| `test_check_only_model_dir_missing` | Missing model-dir → exit 1 |
| `test_check_only_model_dir_no_config` | Dir without config.json → exit 1 |
| `test_check_only_pattern_id` | Mock registry response → resolve path, exit 0 |
| `test_check_only_pattern_id_not_found` | Registry returns `found:false` → exit 1 |
| `test_resolve_model_path_from_registry` | Mock httpx → parse registry_manifest.json, extract model_dir |
| `test_build_vllm_cmd` | Verify arg assembly for `vllm serve <path> --host ... --port ...` |
| `test_bench_stats_computation` | Feed synthetic latencies → verify p50/p95/p99 math |
| `test_bench_empty_results` | No data → sensible output, no division by zero |

## Build order (this session)

1. **scaffold**: pyproject.toml install + logging.py (done)
2. **server.py + cli.py**: registry lookup, vLLM command builder, `--check-only`, `apprentice-serve` entry point
3. **bench.py**: HTTP benchmark client, stats computation, `apprentice-bench` entry point
4. **systemd unit**: `contrib/apprentice-vllm@.service`
5. **tests**: all CPU-only test files
6. **verify**: `apprentice-serve --help`, `apprentice-bench --help`, pytest all pass

## Risks / edge cases

1. **No vLLM installed**: `--check-only` must work without it (using subprocess, no import needed). Runtime launch faiLs gracefully with "vllm not found" error.
2. **Registry unreachable**: Timeout after 5s, clear error message with the registry URL
3. **Model dir without tokenizer**: vLLM will fail at startup — we check for config.json but not full model integrity
4. **Port conflict**: vLLM handles this natively; we pass through its error
5. **GPU out of memory**: vLLM handles this; we pass through its error
