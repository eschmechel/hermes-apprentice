# Multi-Provider Upstream

When no specialist matches a request, the proxy falls back to an upstream API. v0.2 supports multiple upstream providers in a priority-ordered chain.

## Configuration

```yaml
# ~/.apprentice/proxy/upstream_providers.yaml
upstream_providers:
  - name: openrouter
    url: https://openrouter.ai/api/v1
    priority: 1
  - name: fireworks
    url: https://api.fireworks.ai/inference/v1
    priority: 2
  - name: minimax
    url: https://api.minimax.chat/v1
    priority: 3
  - name: together
    url: https://api.together.xyz/v1
    priority: 4
```

All providers must speak the OpenAI-compatible `/v1/chat/completions` protocol.

## CLI flags

```bash
proxy serve \
    --upstream-url https://openrouter.ai/api/v1 \
    --upstream-providers-file ~/.apprentice/proxy/upstream_providers.yaml
```

`--upstream-url` is the primary fallback (OpenRouter). `--upstream-providers-file` adds additional providers tried in priority order. If the primary fails (connection error, 5xx), the proxy tries the next in chain.

## Behavior

```
Request needs upstream (no specialist matched)
       │
       ▼
  Try priority 1: openrouter
       ├─ Success → return response
       └─ Failure → try priority 2
             │
             ├─ Success → return response
             └─ Failure → try priority 3
                   │
                   ...
                   └─ All failed:
                        No specialist match + no upstream configured
                        → HTTP 502 with error message
```

## Safe error handling

If no upstream providers are configured **and** no specialist matches:
- The proxy returns a safe error: `"No specialist matched and no upstream providers configured."`
- This is intentionally non-technical — no leaking of infrastructure details.
- HTTP status: 502 Bad Gateway.

## API key propagation

The `Authorization` header from the incoming request is forwarded to the upstream provider. Each provider's URL must be compatible with the same API key format (`Bearer <key>`).

For OpenRouter, the `HTTP-Referer` header is also forwarded for rate-limit attribution.

## Testing

```bash
# Verify upstream connectivity
curl -X POST http://localhost:8083/v1/chat/completions \
    -H "Authorization: Bearer $OPENROUTER_API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o","messages":[{"role":"user","content":"What is 2+2?"}]}'
```

The response should come from OpenRouter (or from a local specialist if one matches — check `route_decision` in logs).
