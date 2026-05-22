package pairing

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/eschmechel/hermes-apprentice/observer/internal/normalizer"
	"github.com/eschmechel/hermes-apprentice/observer/internal/store"
	_ "modernc.org/sqlite"
)

// newPairer creates an in-memory pairing setup with a fresh store and a stub
// Hermes DB that returns no rows for session lookups. Enrichment failures
// are non-fatal so this is fine for the basic pairing tests.
func newPairer(t *testing.T) (*Pairer, *store.Store) {
	t.Helper()
	dir := t.TempDir()

	s, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Hermes stand-in: an empty SQLite DB with the sessions/messages tables
	// the pairer queries. Empty result == enrichment unavailable, which is
	// the well-tested non-fatal path.
	hermes, err := sql.Open("sqlite", filepath.Join(dir, "hermes.db"))
	if err != nil {
		t.Fatalf("hermes Open: %v", err)
	}
	t.Cleanup(func() { _ = hermes.Close() })
	if _, err := hermes.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, model TEXT, system_prompt TEXT)`); err != nil {
		t.Fatalf("create sessions: %v", err)
	}

	p := New(s, hermes, slog.Default())
	return p, s
}

func TestPairer_BasicUserAssistantPair(t *testing.T) {
	p, s := newPairer(t)
	ctx := context.Background()

	user := normalizer.Normalized{ID: 1, SessionID: "s1", Role: "user", Content: "what is 2+2?", ContentHash: "h1", Timestamp: 100.0}
	asst := normalizer.Normalized{ID: 2, SessionID: "s1", Role: "assistant", Content: "4", ContentHash: "h2", Timestamp: 100.25}

	if err := p.Observe(ctx, user); err != nil {
		t.Fatalf("Observe user: %v", err)
	}
	if err := p.Observe(ctx, asst); err != nil {
		t.Fatalf("Observe assistant: %v", err)
	}

	recs, err := s.Query(ctx, store.QueryOpts{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("len = %d, want 1", len(recs))
	}
	r := recs[0]
	if r.InputText != "what is 2+2?" || r.OutputText != "4" {
		t.Fatalf("texts wrong: %+v", r)
	}
	if r.LatencyMs != 250 {
		t.Fatalf("latency = %d, want 250", r.LatencyMs)
	}
	if r.UserMessageID != 1 || r.AssistantMessageID != 2 {
		t.Fatalf("message ids wrong: %+v", r)
	}
}

func TestPairer_IgnoresToolCallOnlyAssistantTurns(t *testing.T) {
	p, s := newPairer(t)
	ctx := context.Background()

	// Mirrors foundation-06: user, assistant-tool-call (empty content),
	// tool result, assistant-final.
	_ = p.Observe(ctx, normalizer.Normalized{ID: 1, SessionID: "s1", Role: "user", Content: "do the thing", Timestamp: 100.0})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 2, SessionID: "s1", Role: "assistant", Content: "", ToolCalls: `[{"name":"terminal"}]`, Timestamp: 100.05})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 3, SessionID: "s1", Role: "tool", Content: "done", Timestamp: 100.10})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 4, SessionID: "s1", Role: "assistant", Content: "thing done.", Timestamp: 100.15})

	recs, _ := s.Query(ctx, store.QueryOpts{})
	if len(recs) != 1 {
		t.Fatalf("len = %d, want 1 (one final pair, not two)", len(recs))
	}
	r := recs[0]
	if r.UserMessageID != 1 || r.AssistantMessageID != 4 {
		t.Fatalf("wrong ids — pair must skip the tool-call turn: %+v", r)
	}
	if r.OutputText != "thing done." {
		t.Fatalf("output = %q, want final assistant content", r.OutputText)
	}
}

func TestPairer_NewUserOverwritesPendingPair(t *testing.T) {
	p, s := newPairer(t)
	ctx := context.Background()

	_ = p.Observe(ctx, normalizer.Normalized{ID: 1, SessionID: "s1", Role: "user", Content: "first question"})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 2, SessionID: "s1", Role: "user", Content: "actually nevermind, second question"})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 3, SessionID: "s1", Role: "assistant", Content: "ok"})

	recs, _ := s.Query(ctx, store.QueryOpts{})
	if len(recs) != 1 {
		t.Fatalf("len = %d, want 1", len(recs))
	}
	if recs[0].InputText != "actually nevermind, second question" {
		t.Fatalf("expected second user message to win, got: %s", recs[0].InputText)
	}
}

func TestPairer_DifferentSessionsIndependent(t *testing.T) {
	p, s := newPairer(t)
	ctx := context.Background()

	_ = p.Observe(ctx, normalizer.Normalized{ID: 1, SessionID: "s1", Role: "user", Content: "Q1"})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 2, SessionID: "s2", Role: "user", Content: "Q2"})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 3, SessionID: "s1", Role: "assistant", Content: "A1"})
	_ = p.Observe(ctx, normalizer.Normalized{ID: 4, SessionID: "s2", Role: "assistant", Content: "A2"})

	recs, _ := s.Query(ctx, store.QueryOpts{})
	if len(recs) != 2 {
		t.Fatalf("len = %d, want 2", len(recs))
	}
}

func TestPairer_DuplicateInsertIsAbsorbed(t *testing.T) {
	p, s := newPairer(t)
	ctx := context.Background()

	user := normalizer.Normalized{ID: 1, SessionID: "s1", Role: "user", Content: "Q"}
	asst := normalizer.Normalized{ID: 2, SessionID: "s1", Role: "assistant", Content: "A"}

	_ = p.Observe(ctx, user)
	_ = p.Observe(ctx, asst)

	// Replay the same pair (simulating a restart where HWM didn't advance
	// past the assistant message yet).
	_ = p.Observe(ctx, user)
	if err := p.Observe(ctx, asst); err != nil {
		t.Fatalf("duplicate Observe should be silently absorbed, got err: %v", err)
	}

	n, _ := s.CountRecords(ctx)
	if n != 1 {
		t.Fatalf("count = %d, want 1 (UNIQUE constraint must dedupe)", n)
	}
}
