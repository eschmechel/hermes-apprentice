package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleRecord() Record {
	pid := "pattern-1"
	model := "deepseek-v4-flash"
	sph := "sys-hash"
	return Record{
		SessionID:          "sess-1",
		PatternID:          &pid,
		InputHash:          "in-hash-1",
		InputText:          "what is 2+2?",
		OutputText:         "4",
		SystemPromptHash:   &sph,
		ModelUsed:          &model,
		LatencyMs:          250,
		TokenCounts:        json.RawMessage(`{"input":7,"output":1}`),
		CreatedAt:          1700000000,
		UserMessageID:      10,
		AssistantMessageID: 11,
	}
}

func TestInsertAndQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := sampleRecord()
	id, err := s.InsertRecord(ctx, r)
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	if id == 0 {
		t.Fatalf("id = 0, want non-zero")
	}

	got, err := s.Query(ctx, QueryOpts{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].InputText != r.InputText || got[0].OutputText != r.OutputText {
		t.Fatalf("roundtrip mismatch: %+v", got[0])
	}
	if string(got[0].TokenCounts) != string(r.TokenCounts) {
		t.Fatalf("token_counts mismatch: %s vs %s", got[0].TokenCounts, r.TokenCounts)
	}
}

func TestUniqueConstraintRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := sampleRecord()
	if _, err := s.InsertRecord(ctx, r); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := s.InsertRecord(ctx, r)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second insert: err = %v, want ErrDuplicate", err)
	}
}

func TestQueryFilterByPattern(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := sampleRecord()
	_, _ = s.InsertRecord(ctx, r)

	r2 := r
	r2.UserMessageID = 20
	r2.AssistantMessageID = 21
	pid2 := "pattern-2"
	r2.PatternID = &pid2
	_, _ = s.InsertRecord(ctx, r2)

	got, err := s.Query(ctx, QueryOpts{PatternID: "pattern-2"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].PatternID == nil || *got[0].PatternID != "pattern-2" {
		t.Fatalf("pattern filter broken: %+v", got)
	}
}

func TestRecordsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "observer.db")
	ctx := context.Background()

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	if _, err := s1.InsertRecord(ctx, sampleRecord()); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s2.Close()
	got, err := s2.Query(ctx, QueryOpts{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after restart got %d records, want 1", len(got))
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "observer.db")
	// Open and close twice — second open must not double-apply migrations.
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		_ = s.Close()
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open final: %v", err)
	}
	defer s.Close()
	// Should still work for inserts after multiple opens.
	if _, err := s.InsertRecord(context.Background(), sampleRecord()); err != nil {
		t.Fatalf("Insert after multi-open: %v", err)
	}
}
