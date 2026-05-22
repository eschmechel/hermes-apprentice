# Monitoring

Prometheus metrics and Grafana dashboards for real-time observability of the Apprentice deployment.

## Quick start

```bash
# Installer flag
apprentice-setup --apply --enable-monitoring

# Or manually
docker compose -f deploy/docker/compose.monitoring.yml up -d
```

- **Grafana:** http://localhost:3000 (anonymous viewer access)
- **Prometheus:** http://localhost:9090

## Prometheus metrics

The proxy exposes metrics at `GET /metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `apprentice_requests_total` | Counter | Total requests, labeled by `decision` (specialist/upstream/canary_upstream/no_match) and `pattern_id` |
| `apprentice_request_duration_seconds` | Histogram | Request duration, labeled by `route` (specialist/upstream) |
| `apprentice_cost_saved_usd_total` | Counter | Cumulative cost saved by routing to specialists instead of upstream |
| `apprentice_specialist_requests_total` | Counter | Specialist-routed requests by pattern |
| `apprentice_upstream_fallback_total` | Counter | Fallback requests (specialist error/timeout → upstream) |

Scraped every 10 seconds by Prometheus.

## Grafana dashboard

Pre-built 8-panel dashboard at `deploy/grafana/dashboards/apprentice-overview.json`:

| Panel | Type | Visualization |
|-------|------|---------------|
| **Request rate** | Time series | Requests/sec by route decision (specialist vs upstream) |
| **Latency** | Time series | p50, p95, p99 for specialist and upstream routes |
| **Error rate** | Time series | 4xx and 5xx response rate percentage |
| **Cost saved** | Stat | Cumulative USD saved (specialist routing cost avoidance) |
| **Top patterns** | Table | Top 10 most-requested specialist patterns by count |
| **Specialist vs upstream** | Time series | Side-by-side latency comparison over time |
| **Route mix** | Stat / pie | Status donut chart (specialist, upstream, no_match, canary_upstream) |
| **24h counters** | Stat | Request and cost summary in the last 24 hours |

Dashboard auto-provisioned — appears in Grafana immediately on startup.

## Configuration

```bash
proxy serve --metrics-port :9091  # default Prometheus port
```

The proxy's internal metrics handler is always available at `/metrics` when the proxy is running. The monitoring Docker stack adds Prometheus as a scraper and Grafana as a visualization layer.

## Latency percentiles

In addition to Prometheus histograms, the proxy serves rolling percentiles at `GET /stats`:

```json
{
  "specialist": {"p50": 38, "p95": 62, "p99": 98},
  "upstream": {"p50": 420, "p95": 890, "p99": 1450}
}
```

Ring-buffer based — last 1000 requests per route.

## Cost tracking endpoints

```bash
# ROI per pattern
curl http://localhost:8083/api/cost/roi/<pattern-id>

# Usage over time (hour/day/week buckets)
curl http://localhost:8083/api/cost/usage?pattern_id=<id>&bucket=hour

# RunPod live pod costs
curl http://localhost:8083/api/cost/runpod
```

## Canary state

```bash
curl http://localhost:8083/canary/state
```

All pattern ramp states, percentages, and shadow comparison counts. Useful for debugging stuck warming patterns or monitoring broken specialists.

## Logs

Structured JSON log lines at `~/.apprentice/proxy/proxy.log`:

```json
{"time":"2026-05-22T12:00:00Z","route_decision":"specialist","pattern_id":"p1",
 "latency_ms":42,"cost_saved_usd":0.012,"model":"specialist/p1"}
```

Use `proxy summary --state-dir ~/.apprentice/proxy` for aggregated per-pattern reports.

## Alerting

Canary demotions and budget threshold crossings trigger Telegram alerts (see [Canary & Safety](Canary-&-Safety) and [Budget & Cost](Budget-&-Cost)).
