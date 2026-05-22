# Quickstart

Five-minute smoke test of the full pipeline.

## 1. Install (once)

```bash
bash .firecracker/bootstrap.sh && .firecracker/vm.sh start
apprentice-setup --apply
```

See [Setup Guide](Setup-Guide) for details.

## 2. Demo (full pipeline, no GPU needed)

```bash
bash scripts/demo-run.sh
```

This runs offline: seeds a session log → detection → pipeline → promotion → serving → test request. All steps visible in terminal output.

## 3. Live pipeline (requires GPU)

```bash
# Start capture services
observer serve --listen :8081 --hermes-db ~/.hermes/state.db &
detector serve --listen :8082 --observer-url http://localhost:8081 &

# Build dataset for a detected pattern
dataset-builder build --pattern-id <id> --observer-url http://localhost:8081

# Train + merge + validate (one command)
apprentice-orchestrator tick

# Serve the promoted specialist
apprentice-serve --model-dir ~/.apprentice/registry/<id>/latest/ --port 8000 &

# Start the proxy
proxy serve --listen :8083 --upstream-url https://openrouter.ai/api/v1

# Point Hermes at the proxy: http://HOST_IP:8083/v1/chat/completions
```

## 4. Verify

```bash
curl http://localhost:8083/healthz           # proxy alive
curl http://localhost:8083/canary/state      # canary status
curl http://localhost:8001/residency/status  # serving residency
curl http://localhost:8082/registry/<id>/latest  # latest promoted model
```
