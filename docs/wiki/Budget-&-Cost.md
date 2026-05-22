# Budget & Cost

Monetary budget system with Telegram-based increase flow. Controls cloud GPU spend at 80%, 95%, and 100% thresholds.

## How it works

```
Every cloud spend (RunPod provision, teacher augmentation, upstream fallback)
       │
       ▼
  budget.record(tenant, amount_usd, source)
       │
       ▼
  budget.check_budget(tenant)
       │
       ├─ < 80%  → allowed
       ├─ > 80%  → allowed + Telegram warning
       ├─ > 95%  → cloud paused + Telegram alert
       └─ ≥ 100% → all cloud blocked + Telegram alert
```

## Configuration

### Set budget

```bash
# Via CLI
apprentice-orchestrator budget set --tenant acme --monthly 100

# Via installer (initial setup)
apprentice-setup --apply --monthly-budget 100

# Via MCP
get_budget(tenant="acme")
set_budget(tenant="acme", monthly_budget_usd=100)
```

### View budget status

```bash
apprentice-orchestrator budget get --tenant acme
# Returns: {monthly_budget: 100, spent: 45.20, remaining: 54.80, percent: 45.2}
```

## Threshold alerts

| Threshold | Action |
|-----------|--------|
| **> 80%** | Telegram message: "Budget at 82%. Reply `budget increase <amount>` to raise, or `budget ignore` to dismiss." |
| **> 95%** | Cloud paused. Telegram message: "Budget critical at 97%. Reply `budget increase <amount>` to resume." |
| **100%** | All cloud blocked. Telegram message: "Budget exhausted. Reply `budget increase <amount>` to add funds." |

## Telegram increase flow

1. System detects threshold crossed, enqueues budget alert to Telegram outbox.
2. Hermes cron delivers the message to the operator channel.
3. Operator replies: `budget increase 50`
4. Reply poller parses the command, writes decision marker.
5. Orchestrator processes: `budget_increase(tenant, 50)` → adds $50 to monthly budget.
6. If cloud was paused/blocked, it resumes immediately.

### CLI

```bash
apprentice-orchestrator budget increase --tenant acme --amount 50
```

### MCP

```
budget_increase(tenant="acme", amount=50)
```

## Budget history

```bash
apprentice-orchestrator budget history --tenant acme
```

Returns the full JSONL ledger: timestamps, amounts, sources (runpod, teacher, upstream), and running totals.

## Cost tracking

All cloud spend is tracked in `~/.apprentice/tenants/<tenant>/budget.jsonl`:

```json
{"time":"2026-05-22T12:00:00Z","amount":1.23,"source":"runpod","description":"A100 training run"}
{"time":"2026-05-22T14:00:00Z","amount":50.00,"source":"budget_increase","description":"Operator approved increase"}
```

Training costs are recorded separately in `~/.apprentice/cost/ledger.jsonl` for ROI analysis. Per-request savings (specialist vs upstream cost difference) are logged in `~/.apprentice/proxy/proxy.log`.

## Billing period

Budget resets at the start of each calendar month. Unused budget does not roll over.

See [Multi-Tenancy](Multi-Tenancy) for the resource quota system.
