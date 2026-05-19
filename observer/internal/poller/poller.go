// Package poller tails the Hermes session DB and emits new `messages` rows.
//
// The reader uses SQLite in WAL mode (Hermes itself sets WAL — see
// hermes_state.py:208 _execute_write). WAL guarantees readers never block
// writers and vice versa, so a 1-second-poll observer can sit alongside an
// actively-writing Hermes process without contention.
package poller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

type Config struct {
	HermesDBPath string
	Interval     time.Duration
	Logger       *slog.Logger
	// Handler is called for each new message in id-ascending order. Returning
	// an error stops the poller. If nil, messages are only logged.
	Handler func(context.Context, Message) error
	// StartFromID overrides the initial high-water mark. Use -1 (the default
	// when zero is meaningful) to start from MAX(id) — i.e. only stream
	// messages that arrive AFTER startup. Use 0 to backfill everything in
	// the DB (handy for first-run reconciliation and acceptance testing).
	StartFromID int64
	// StartFromBeginning, when true, ignores StartFromID and starts at 0.
	StartFromBeginning bool
}

type Poller struct {
	cfg    Config
	logger *slog.Logger
}

func New(cfg Config) *Poller {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	return &Poller{cfg: cfg, logger: cfg.Logger}
}

// Run polls until ctx is cancelled. Returns ctx.Err() on shutdown; non-nil on
// terminal DB failures.
func (p *Poller) Run(ctx context.Context) error {
	// mode=ro + _journal_mode=WAL: we never write to the Hermes DB. WAL mode
	// is already set by Hermes on its writer connection; specifying it here
	// is a no-op for an existing DB but documents intent.
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL", p.cfg.HermesDBPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open hermes db: %w", err)
	}
	defer db.Close()

	// Verify the DB and schema are reachable up front so the poller fails
	// fast rather than silently looping on a typoed --hermes-db path.
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping hermes db: %w", err)
	}

	var lastID int64
	switch {
	case p.cfg.StartFromBeginning:
		lastID = 0
	case p.cfg.StartFromID > 0:
		lastID = p.cfg.StartFromID
	default:
		if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM messages").Scan(&lastID); err != nil {
			return fmt.Errorf("initial high-water mark: %w", err)
		}
	}
	p.logger.Info("poller starting", "hermes_db", p.cfg.HermesDBPath, "interval", p.cfg.Interval, "start_at_id", lastID)

	tick := time.NewTicker(p.cfg.Interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			n, newLast, err := p.drain(ctx, db, lastID)
			if err != nil {
				// Transient SQLite errors (locked, busy) shouldn't kill the
				// poller — log and retry on the next tick.
				if isTransient(err) {
					p.logger.Warn("transient db error, will retry", "err", err)
					continue
				}
				return err
			}
			if n > 0 {
				p.logger.Debug("polled", "new", n, "high_water", newLast)
				lastID = newLast
			}
		}
	}
}

func (p *Poller) drain(ctx context.Context, db *sql.DB, sinceID int64) (int, int64, error) {
	const q = `
SELECT id, session_id, role, content, timestamp, tool_calls, tool_name
FROM messages
WHERE id > ?
ORDER BY id ASC`
	rows, err := db.QueryContext(ctx, q, sinceID)
	if err != nil {
		return 0, sinceID, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	count := 0
	high := sinceID
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Timestamp, &m.ToolCalls, &m.ToolName); err != nil {
			return count, high, fmt.Errorf("scan: %w", err)
		}
		if p.cfg.Handler != nil {
			if err := p.cfg.Handler(ctx, m); err != nil {
				// Don't advance past the failed message — the handler can
				// retry it on the next tick.
				return count, high, fmt.Errorf("handler: %w", err)
			}
		} else {
			// No handler wired (e.g. early scaffold testing): log raw to make
			// the poller visibly useful on its own.
			p.logger.Info("hermes message (no handler)",
				"id", m.ID,
				"session_id", m.SessionID,
				"role", m.Role,
				"content_len", contentLen(m.Content),
				"tool_calls_len", contentLen(m.ToolCalls),
				"ts", m.Timestamp,
			)
		}
		count++
		high = m.ID
	}
	if err := rows.Err(); err != nil {
		return count, high, fmt.Errorf("row iter: %w", err)
	}
	return count, high, nil
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc.org/sqlite surfaces SQLITE_BUSY/SQLITE_LOCKED as plain error
	// strings. We don't import the driver's error type to avoid coupling.
	return errors.Is(err, context.DeadlineExceeded) ||
		containsAny(msg, "database is locked", "SQLITE_BUSY", "SQLITE_LOCKED")
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}

func contentLen(s sql.NullString) int {
	if !s.Valid {
		return 0
	}
	return len(s.String)
}

func nullStr(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}
