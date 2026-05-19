package store

import (
	"context"
	"fmt"
)

// migrations is the ordered list of schema migrations. Index N corresponds
// to schema version N+1 (so migrations[0] takes a fresh DB to version 1).
// Append-only — never reorder or rewrite a published migration.
var migrations = []string{
	// v1: records table + indexes
	`
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS records (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id           TEXT NOT NULL,
    pattern_id           TEXT,
    input_hash           TEXT NOT NULL,
    input_text           TEXT NOT NULL,
    output_text          TEXT NOT NULL,
    system_prompt_hash   TEXT,
    model_used           TEXT,
    latency_ms           INTEGER NOT NULL DEFAULT 0,
    token_counts         TEXT,
    created_at           REAL NOT NULL,
    user_message_id      INTEGER NOT NULL,
    assistant_message_id INTEGER NOT NULL,
    UNIQUE(session_id, user_message_id, assistant_message_id)
);

CREATE INDEX IF NOT EXISTS idx_records_pattern  ON records(pattern_id);
CREATE INDEX IF NOT EXISTS idx_records_session  ON records(session_id);
CREATE INDEX IF NOT EXISTS idx_records_created  ON records(created_at DESC);
`,
}

func (s *Store) migrate(ctx context.Context) error {
	// Read current version (rows in schema_version are append-only; latest = MAX).
	// If schema_version doesn't exist yet (fresh DB), MAX query returns NULL → 0.
	var current int
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE((SELECT MAX(version) FROM schema_version), 0)
`).Scan(&current)
	if err != nil {
		// Most likely cause: schema_version table doesn't exist. We'll create
		// it in the first migration; treat as version 0.
		current = 0
	}

	for i, sqlScript := range migrations {
		target := i + 1
		if current >= target {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx for v%d: %w", target, err)
		}
		if _, err := tx.ExecContext(ctx, sqlScript); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply v%d: %w", target, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", target); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record v%d: %w", target, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v%d: %w", target, err)
		}
		current = target
	}
	return nil
}
