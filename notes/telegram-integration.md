# Telegram integration (telegram-01 .. telegram-06)

This wiring delivers three message kinds to a configured Telegram channel
and converts user replies into on-disk decision markers the orchestrator
consumes.

```
host (or microVM)                       microVM (Hermes)
─────────────────────────               ─────────────────────────────
detector / validator / proxy            hermes cron --no-agent
  │                                       --script apprentice-telegram-dispatch.sh
  │ apprentice-telegram enqueue ...       --deliver telegram "every 5m"
  ▼                                       │
~/.apprentice/outbox/                     │  every 5 min:
  20260519T123456Z-graduation-…txt        │    1. dispatch-one prints oldest pending body to stdout
  20260519T124500Z-failure-…txt           │    2. Hermes captures stdout, sends to TELEGRAM_HOME_CHANNEL
  20260519T130000Z-weekly-…txt            │    3. dispatch-one moves file → processed/
                                          ▼
                                        Telegram
                                          ▲
                                          │ user replies ("train gc-abcd1234")
                                          │
                                        hermes cron --no-agent
                                          --script apprentice-telegram-poll.sh
                                          --deliver telegram "every 1m"
                                          │
                                          ▼
                                        ~/.apprentice/decisions/
                                          20260519T125000Z-train-gc-abcd1234.json
                                          20260519T125500Z-skip-gc-deadbeef.json
                                        orchestrator picks these up on its tick
```

## Outbox layout

```
~/.apprentice/outbox/
    <utc-ts>-<kind>-<pattern_id>.txt   ← pending
    .inflight/                          ← in-flight dispatch (recovered on crash)
    processed/                          ← acked
```

## Message kinds & templates

All three are in `telegram/src/apprentice_telegram/templates.py`. Bodies are
plain text (Telegram default parse mode) — no Markdown, no HTML escaping
games. Each message ends with a single trailing newline.

| Kind | Producer | Template |
|---|---|---|
| graduation | detector (after `record_count` ≥ threshold) | `render_graduation` |
| failure | validator (when promotion gate refuses) | `render_failure` |
| weekly | instrumentation summary (Sunday cron) | `render_weekly` |

Graduation messages carry a short correlation id `[gc-<8 hex>]` derived
deterministically from `(pattern_id, candidate_salt)`. Replies referencing
that id route to the right decision marker; replies that omit it fall back
to "the most recent unacked graduation in the outbox".

## Enqueueing from host services

```bash
# After detector clusters a new pattern:
apprentice-telegram enqueue graduation \
    --pattern-id abc-123 \
    --record-count 42 \
    --description "Extract SKU and qty from order-confirmation emails." \
    --example "Order #4421 — confirm SKU AX-7 qty 3." \
    --example "Pull qty for SKU MX-2 from yesterday."

# After validator refuses a promotion:
apprentice-telegram enqueue failure \
    --validator-result ~/.apprentice/failures/abc-123/result.json \
    --failure-report ~/.apprentice/failures/abc-123/report.md

# Weekly Sunday summary (driven by the proxy `summary` subcommand):
proxy summary --since 7d --logs ~/.apprentice/proxy/log --out /tmp/wk.json
apprentice-telegram enqueue weekly --summary-json /tmp/wk.json
```

The validator already calls into `skill_registrar` after a successful
promote (see `notes/skill-registration.md`). The Telegram failure-enqueue is
intentionally a separate CLI call — keeping the validator free of Telegram
imports lets the test suite stay synchronous and network-free.

## Dispatch via Hermes cron

One-time setup inside the microVM:

```bash
ssh root@10.0.2.2 'mkdir -p /root/.hermes/scripts'
scp telegram/scripts/apprentice-telegram-dispatch.sh \
    root@10.0.2.2:/root/.hermes/scripts/apprentice-telegram-dispatch.sh
ssh root@10.0.2.2 'chmod +x /root/.hermes/scripts/apprentice-telegram-dispatch.sh'

ssh root@10.0.2.2 'hermes cron create \
    --name apprentice-telegram \
    --no-agent \
    --script apprentice-telegram-dispatch.sh \
    --deliver telegram "every 5m"'
```

Empty outbox → empty stdout → Hermes' no-agent watchdog treats it as a
silent tick (see `notes/cron-tick-implementation.md`, section "Output
handling"). The 5-minute cadence is a cheap idle poll.

## Reply polling

```bash
# Install once:
scp telegram/scripts/apprentice-telegram-poll.sh \
    root@10.0.2.2:/root/.hermes/scripts/apprentice-telegram-poll.sh
ssh root@10.0.2.2 'chmod +x /root/.hermes/scripts/apprentice-telegram-poll.sh'

ssh root@10.0.2.2 'hermes cron create \
    --name apprentice-poll-replies \
    --no-agent \
    --script apprentice-telegram-poll.sh \
    --deliver telegram "every 1m"'
```

Each tick the script calls `apprentice-telegram poll-replies`, which:

1. Reads the saved `update_id` offset from
   `~/.apprentice/telegram/updates_offset`.
2. Hits `https://api.telegram.org/bot<token>/getUpdates?offset=<N>&allowed_updates=["message"]`.
3. Parses each message text against `^(train|details|skip)\b(\s+gc-[0-9a-f]{8})?`.
4. For recognised replies, writes a JSON marker under
   `~/.apprentice/decisions/<utc>-<action>-<cid>.json`.
5. For `details`: also enqueues a preview-stub message back into the outbox
   so the next dispatch tick delivers feedback.
6. Advances the offset past every update id seen (recognised or not).

The bot token must be present as `TELEGRAM_BOT_TOKEN` in the cron script's
env (Hermes loads `~/.hermes/.env` automatically per
`notes/profiles-implementation.md`).

## Decision marker format

```json
{
  "action": "train",
  "cid": "gc-abcd1234",
  "chat_id": 8780042950,
  "message_id": 1234,
  "user_id": 99,
  "update_id": 100,
  "raw_text": "train gc-abcd1234",
  "received_at": "2026-05-19T12:50:00Z"
}
```

The orchestrator (next milestone) reads markers oldest-first, dispatches:

- `train` → kick off `apprentice-train` for the pattern referenced by `cid`.
- `details` → write the dataset preview to the outbox (handled here as a
  best-effort stub; the orchestrator should overwrite it with the real
  preview once the dataset is materialised).
- `skip` → mark the pattern's status as `rejected` in the detector's
  pattern store.

## Phrasing notes (telegram-06)

The templates aim for log-line tone:

- Numbers first, prose second.
- No emoji, no "" or "🎉".
- Failure messages quote the actual scores; refusal sentence is one line.
- Weekly summary uses ` $X ` for money, ` ~Xs ` for time saved — easy to
  scan on mobile.
- Action verbs (`train`, `details`, `skip`) appear lowercase + inline so a
  reply is one short word + paste of the cid.
