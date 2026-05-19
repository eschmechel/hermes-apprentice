// Package store is the observer's local SQLite store for normalized (input,
// output) records. The schema is the contract for downstream apprentice
// services — the Detector reads records to mine patterns, and the Dataset
// Builder reads them (filtered by pattern_id) to assemble training JSONL.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Record is one normalized (user, assistant) pair captured from a Hermes
// session. JSON tags are the GET /records wire format.
type Record struct {
	ID                 int64           `json:"id"`
	SessionID          string          `json:"session_id"`
	PatternID          *string         `json:"pattern_id,omitempty"`
	InputHash          string          `json:"input_hash"`
	InputText          string          `json:"input_text"`
	OutputText         string          `json:"output_text"`
	SystemPromptHash   *string         `json:"system_prompt_hash,omitempty"`
	ModelUsed          *string         `json:"model_used,omitempty"`
	LatencyMs          int64           `json:"latency_ms"`
	TokenCounts        json.RawMessage `json:"token_counts,omitempty"`
	CreatedAt          float64         `json:"created_at"`
	UserMessageID      int64           `json:"user_message_id"`
	AssistantMessageID int64           `json:"assistant_message_id"`
}

// Store wraps the observer SQLite DB.
type Store struct {
	db *sql.DB
}

// Open creates (if needed) the observer DB at path and applies migrations.
// Parent directory is created with mode 0755 if missing.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir parent dir: %w", err)
	}
	// _journal_mode=WAL → concurrent readers don't block writers and vice
	// versa; helpful when observer-05's HTTP handlers read while the pairer
	// is mid-insert. _busy_timeout=2000 keeps insert retries short under
	// contention rather than failing outright.
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=2000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open observer db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping observer db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// InsertRecord persists r. The UNIQUE(session_id, user_message_id,
// assistant_message_id) constraint silently absorbs duplicate inserts (e.g.
// HWM-replay after a crash before HWM was advanced past the message) by
// returning ErrDuplicate rather than failing the call site.
func (s *Store) InsertRecord(ctx context.Context, r Record) (int64, error) {
	const q = `
INSERT INTO records (
    session_id, pattern_id, input_hash, input_text, output_text,
    system_prompt_hash, model_used, latency_ms, token_counts, created_at,
    user_message_id, assistant_message_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, q,
		r.SessionID, r.PatternID, r.InputHash, r.InputText, r.OutputText,
		r.SystemPromptHash, r.ModelUsed, r.LatencyMs, []byte(r.TokenCounts), r.CreatedAt,
		r.UserMessageID, r.AssistantMessageID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrDuplicate
		}
		return 0, fmt.Errorf("insert record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// QueryOpts is the filter set accepted by Query. All fields are optional.
type QueryOpts struct {
	SincePosix float64 // only return records with created_at >= this value
	PatternID  string  // only return records whose pattern_id == this
	Limit      int     // max rows; defaults to 100 if <= 0
}

// Query returns records matching opts, ordered by created_at DESC.
func (s *Store) Query(ctx context.Context, opts QueryOpts) ([]Record, error) {
	args := []any{}
	where := ""
	addClause := func(clause string, vals ...any) {
		if where == "" {
			where = " WHERE " + clause
		} else {
			where += " AND " + clause
		}
		args = append(args, vals...)
	}
	if opts.SincePosix > 0 {
		addClause("created_at >= ?", opts.SincePosix)
	}
	if opts.PatternID != "" {
		addClause("pattern_id = ?", opts.PatternID)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)

	q := `
SELECT id, session_id, pattern_id, input_hash, input_text, output_text,
       system_prompt_hash, model_used, latency_ms, token_counts, created_at,
       user_message_id, assistant_message_id
FROM records` + where + `
ORDER BY created_at DESC
LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query records: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var tokenCountsBlob []byte
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.PatternID, &r.InputHash, &r.InputText, &r.OutputText,
			&r.SystemPromptHash, &r.ModelUsed, &r.LatencyMs, &tokenCountsBlob, &r.CreatedAt,
			&r.UserMessageID, &r.AssistantMessageID,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if len(tokenCountsBlob) > 0 {
			r.TokenCounts = json.RawMessage(tokenCountsBlob)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountRecords returns the total number of records. Useful for tests + telemetry.
func (s *Store) CountRecords(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records").Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

var ErrDuplicate = errors.New("duplicate record (UNIQUE constraint)")

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "UNIQUE constraint failed") || contains(msg, "constraint failed: UNIQUE")
}

func contains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
