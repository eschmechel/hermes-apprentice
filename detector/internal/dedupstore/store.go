// Package dedupstore persists "we already embedded this input" markers so a
// detector restart doesn't re-embed every record the observer has accumulated.
// The freshness window (default 24h per the detector-02 spec) defines how
// long a hash stays "seen" before we'd re-embed.
package dedupstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	freshness time.Duration
	now       func() time.Time
}

// Open creates (if needed) the dedup DB at path and applies the v1 schema.
// freshness defines the "seen within" window for ContainsFresh / MarkSeen
// short-circuit logic. now is injectable for tests; nil → time.Now.
func Open(path string, freshness time.Duration, now func() time.Time) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir parent: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=2000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	s := &Store{db: db, freshness: freshness, now: now}
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

func (s *Store) migrate(ctx context.Context) error {
	const v1 = `
CREATE TABLE IF NOT EXISTS seen (
    input_hash   TEXT PRIMARY KEY,
    first_seen   REAL NOT NULL,
    last_seen    REAL NOT NULL,
    record_count INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_seen_last ON seen(last_seen DESC);
`
	_, err := s.db.ExecContext(ctx, v1)
	return err
}

// ContainsFresh reports whether inputHash has been seen within the freshness
// window. A stale entry returns false — the caller should re-embed.
func (s *Store) ContainsFresh(ctx context.Context, inputHash string) (bool, error) {
	var lastSeen sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		"SELECT last_seen FROM seen WHERE input_hash = ?", inputHash).Scan(&lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !lastSeen.Valid {
		return false, nil
	}
	seenAt := time.Unix(int64(lastSeen.Float64), 0)
	return s.now().Sub(seenAt) <= s.freshness, nil
}

// MarkSeen upserts the hash with last_seen = now and bumps record_count.
func (s *Store) MarkSeen(ctx context.Context, inputHash string) error {
	now := float64(s.now().Unix())
	_, err := s.db.ExecContext(ctx, `
INSERT INTO seen (input_hash, first_seen, last_seen, record_count)
VALUES (?, ?, ?, 1)
ON CONFLICT(input_hash) DO UPDATE SET
    last_seen = excluded.last_seen,
    record_count = seen.record_count + 1
`, inputHash, now, now)
	return err
}

// SkipOrMark is the typical caller's one-shot: returns true if we've seen
// this hash inside the freshness window (caller should skip embedding); else
// records the hash as seen now and returns false.
func (s *Store) SkipOrMark(ctx context.Context, inputHash string) (bool, error) {
	fresh, err := s.ContainsFresh(ctx, inputHash)
	if err != nil {
		return false, err
	}
	if fresh {
		return true, s.MarkSeen(ctx, inputHash)
	}
	return false, s.MarkSeen(ctx, inputHash)
}

// Count returns the total number of seen hashes (fresh or stale).
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM seen").Scan(&n)
	return n, err
}

// PruneStale deletes entries where last_seen is older than freshness*pruneFactor.
// pruneFactor lets the operator be more aggressive (1.0 = exactly freshness,
// 7.0 = a week beyond the active window). Returns rows deleted.
func (s *Store) PruneStale(ctx context.Context, pruneFactor float64) (int64, error) {
	cutoff := float64(s.now().Add(-time.Duration(float64(s.freshness) * pruneFactor)).Unix())
	res, err := s.db.ExecContext(ctx, "DELETE FROM seen WHERE last_seen < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
