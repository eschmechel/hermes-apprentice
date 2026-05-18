# Hermes session DB schema

Source: `hermes_state.py` in Hermes v0.14.0 (release tag `v2026.5.16`, commit `a91a57fa`). Path inside the Firecracker rootfs: `/usr/local/lib/hermes-agent/hermes_state.py`.

- Engine: **SQLite** with WAL mode, FTS5 enabled, application-level retry with jitter for write contention.
- Default DB path: `~/.hermes/state.db` (driven by `hermes_constants.get_hermes_home()`).
- Schema version constant: `SCHEMA_VERSION = 11`. Migrations follow the Beets/sqlite-utils pattern — `SCHEMA_SQL` is the single source of truth, `_reconcile_columns` diffs live tables against it and `ADD COLUMN`s anything missing on startup.

## Tables

### `sessions`

One row per conversation. Tracks identity, model/billing context, lifecycle, and rolled-up usage counters.

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PRIMARY KEY | Session identifier (caller-supplied; typically a UUID or gateway-generated key) |
| `source` | TEXT NOT NULL | Where the session originated — e.g. CLI, gateway platform, sub-agent |
| `user_id` | TEXT | Optional user identifier for multi-user gateways |
| `model` | TEXT | Model name (e.g. `claude-opus-4-7`) |
| `model_config` | TEXT | JSON blob of model parameters used for this session |
| `system_prompt` | TEXT | The active system prompt for the session |
| `parent_session_id` | TEXT | Self-FK to `sessions.id` — set when this session was forked from another (curator review forks, sub-agent spawns, etc.) |
| `started_at` | REAL NOT NULL | Unix epoch seconds (REAL = float, sub-second precision) |
| `ended_at` | REAL | NULL while open; epoch seconds at close |
| `end_reason` | TEXT | Free-form string set by `end_session(end_reason=...)` |
| `message_count` | INTEGER DEFAULT 0 | Rolled-up counter |
| `tool_call_count` | INTEGER DEFAULT 0 | Rolled-up counter |
| `input_tokens` | INTEGER DEFAULT 0 | Cumulative |
| `output_tokens` | INTEGER DEFAULT 0 | Cumulative |
| `cache_read_tokens` | INTEGER DEFAULT 0 | Cumulative (prompt caching) |
| `cache_write_tokens` | INTEGER DEFAULT 0 | Cumulative (prompt caching) |
| `reasoning_tokens` | INTEGER DEFAULT 0 | Cumulative (extended thinking / reasoning models) |
| `billing_provider` | TEXT | e.g. `anthropic`, `openai`, `openrouter` |
| `billing_base_url` | TEXT | Provider endpoint (lets a session record which gateway it actually billed against) |
| `billing_mode` | TEXT | Provider's billing mode tag (free tier, paid, etc.) |
| `estimated_cost_usd` | REAL | Pre-call estimate |
| `actual_cost_usd` | REAL | Post-call truth, if the provider reported one |
| `cost_status` | TEXT | Lifecycle of the cost record (e.g. `estimated`, `confirmed`, `unbilled`) |
| `cost_source` | TEXT | Where `actual_cost_usd` came from (provider API, manual entry, etc.) |
| `pricing_version` | TEXT | Pricing table version used for the estimate — lets you re-cost historical sessions if rates change |
| `title` | TEXT | Display title (often inferred from the first user message) |
| `api_call_count` | INTEGER DEFAULT 0 | Number of provider API round-trips made for this session |

Foreign key: `parent_session_id → sessions(id)`.

### `messages`

One row per turn (user, assistant, system, tool). Append-only in normal use.

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER PRIMARY KEY AUTOINCREMENT | Monotonic, used as `messages_fts.rowid` |
| `session_id` | TEXT NOT NULL REFERENCES sessions(id) | FK to `sessions.id` |
| `role` | TEXT NOT NULL | `user`, `assistant`, `system`, `tool` |
| `content` | TEXT | Message text (NULL for tool-call-only assistant turns) |
| `tool_call_id` | TEXT | For role=`tool` rows: which tool_call this message satisfies |
| `tool_calls` | TEXT | JSON array of tool calls emitted by an assistant turn |
| `tool_name` | TEXT | Convenience denormalization of the tool name for FTS |
| `timestamp` | REAL NOT NULL | Unix epoch seconds |
| `token_count` | INTEGER | Per-message token count when known |
| `finish_reason` | TEXT | Provider's stop reason (`stop`, `length`, `tool_calls`, etc.) |
| `reasoning` | TEXT | Legacy/aggregated reasoning text |
| `reasoning_content` | TEXT | Anthropic-style extended thinking content block |
| `reasoning_details` | TEXT | Provider-specific reasoning metadata (JSON) |
| `codex_reasoning_items` | TEXT | OpenAI Codex-style reasoning items (JSON) |
| `codex_message_items` | TEXT | OpenAI Codex-style message items (JSON) |

Foreign key: `session_id → sessions(id)`.

### `state_meta`

Plain key/value table for misc runtime state (e.g. last checkpoint, feature flags). Two TEXT columns: `key` (PK) and `value`.

### `schema_version`

Single row holding the current `SCHEMA_VERSION` integer. Used at startup to gate migration logic in `_init_schema` / `_reconcile_columns`.

## Indexes

| Index | Columns | Purpose |
|---|---|---|
| `idx_sessions_source` | `sessions(source)` | List sessions by gateway/platform |
| `idx_sessions_parent` | `sessions(parent_session_id)` | Walk parent→children for forked sessions |
| `idx_sessions_started` | `sessions(started_at DESC)` | Recent-sessions listings |
| `idx_messages_session` | `messages(session_id, timestamp)` | Replay a session in time order |

## Full-text search

Two FTS5 virtual tables stay in sync with `messages` via triggers:

| Table | Tokenizer | Use case |
|---|---|---|
| `messages_fts` | default (`unicode61`) | English / Latin-script substring + phrase search |
| `messages_fts_trigram` | `trigram` | CJK / Thai / non-segmented scripts — the default tokenizer splits CJK into individual characters and breaks phrase matching; trigram tokenization creates overlapping 3-byte sequences for substring queries |

Both tables index a synthetic content blob:
```sql
COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')
```
This concatenation means searches hit message text, tool names, AND raw tool-call JSON — useful for "find every session where we called `terminal()` with `npm install`."

Six triggers maintain consistency:
- `messages_fts_insert` / `messages_fts_trigram_insert` — fan out new rows
- `messages_fts_delete` / `messages_fts_trigram_delete` — drop on delete
- `messages_fts_update` / `messages_fts_trigram_update` — delete+reinsert on update

`messages_fts.rowid` and `messages_fts_trigram.rowid` mirror `messages.id`, so you can join back:
```sql
SELECT m.* FROM messages m
JOIN messages_fts f ON f.rowid = m.id
WHERE messages_fts MATCH 'install OR upgrade';
```

## Relationships at a glance

```
sessions ──┬─< messages (session_id → id)
           └─< sessions (parent_session_id → id, self-FK for forks)

messages ──┬─< messages_fts (rowid → id, via trigger)
           └─< messages_fts_trigram (rowid → id, via trigger)
```

## Concurrency notes (relevant if multiple agents will hit this DB)

- WAL mode is enabled at connection time.
- SQLite `timeout=1.0` is intentionally short — write contention is handled in application code via `_execute_write` with `_WRITE_MAX_RETRIES = 15` retries and random jitter between 20 ms and 150 ms.
- Reason (per comments at line 167–175): with multiple Hermes processes (gateway + CLI sessions + worktree agents) sharing one `state.db`, SQLite's deterministic busy-handler backoff produces convoy effects. Jittered retry naturally staggers writers.
- A PASSIVE WAL checkpoint runs every 50 writes (`_CHECKPOINT_EVERY_N_WRITES = 50`).

## Implications for Apprentice

- The Observer can tail `messages` ordered by `(session_id, timestamp)` using `idx_messages_session` — no full scan needed.
- The Detector can filter on `messages.role`, `messages.tool_name`, or `messages_fts MATCH` to find specific event patterns cheaply.
- `sessions.source` is the natural shard key when training data is being collected across multiple Hermes deployments.
- `sessions.parent_session_id` makes it possible to recover the full lineage of a forked agent run — relevant if the Apprentice trains on curator-fork outcomes vs. main-trunk ones.
- `cache_*_tokens` and `actual_cost_usd` are populated, so cost-aware data filtering (e.g. "only use sessions under $X") is trivial.
