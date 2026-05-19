package poller

import "database/sql"

// Message mirrors the columns selected from the Hermes messages table.
//
// Schema reference: hermes_state.py SCHEMA_SQL, table `messages`.
// We pick the fields the downstream apprentice pipeline cares about today;
// reasoning/codex fields are intentionally skipped — they're large and
// observer-03's normalizer doesn't need them yet.
type Message struct {
	ID         int64
	SessionID  string
	Role       string
	Content    sql.NullString
	Timestamp  float64
	ToolCalls  sql.NullString
	ToolName   sql.NullString
	TokenCount sql.NullInt64 // messages.token_count (per-message, nullable)
}
