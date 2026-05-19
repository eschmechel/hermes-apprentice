package dedupstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestStore(t *testing.T, freshness time.Duration) (*Store, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	s, err := Open(filepath.Join(t.TempDir(), "dedup.db"), freshness, clock.Now)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clock
}

func TestSkipOrMark_FreshSecondHitIsSkipped(t *testing.T) {
	s, clock := newTestStore(t, 24*time.Hour)
	ctx := context.Background()

	skip, err := s.SkipOrMark(ctx, "hash-A")
	if err != nil || skip {
		t.Fatalf("first call should NOT skip; skip=%v err=%v", skip, err)
	}
	clock.advance(1 * time.Hour)
	skip, err = s.SkipOrMark(ctx, "hash-A")
	if err != nil || !skip {
		t.Fatalf("second call within freshness should skip; skip=%v err=%v", skip, err)
	}
}

func TestSkipOrMark_StaleEntryIsRefreshed(t *testing.T) {
	s, clock := newTestStore(t, 24*time.Hour)
	ctx := context.Background()

	_, _ = s.SkipOrMark(ctx, "hash-A")
	clock.advance(25 * time.Hour)
	skip, err := s.SkipOrMark(ctx, "hash-A")
	if err != nil || skip {
		t.Fatalf("stale entry should NOT skip on next observation; skip=%v err=%v", skip, err)
	}
}

func TestSkipOrMark_DifferentHashesIndependent(t *testing.T) {
	s, _ := newTestStore(t, 24*time.Hour)
	ctx := context.Background()

	_, _ = s.SkipOrMark(ctx, "hash-A")
	skip, _ := s.SkipOrMark(ctx, "hash-B")
	if skip {
		t.Fatalf("distinct hashes should not collide")
	}
}

func TestMarkSeen_BumpsRecordCount(t *testing.T) {
	s, _ := newTestStore(t, 24*time.Hour)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.MarkSeen(ctx, "h"); err != nil {
			t.Fatalf("MarkSeen %d: %v", i, err)
		}
	}
	var rc int
	if err := s.db.QueryRow("SELECT record_count FROM seen WHERE input_hash = 'h'").Scan(&rc); err != nil {
		t.Fatalf("scan record_count: %v", err)
	}
	if rc != 3 {
		t.Fatalf("record_count = %d, want 3", rc)
	}
}

func TestPruneStale_RemovesOnlyOldEntries(t *testing.T) {
	s, clock := newTestStore(t, 1*time.Hour)
	ctx := context.Background()

	_ = s.MarkSeen(ctx, "old")
	clock.advance(3 * time.Hour)
	_ = s.MarkSeen(ctx, "fresh")

	deleted, err := s.PruneStale(ctx, 1.5)
	if err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	n, _ := s.Count(ctx)
	if n != 1 {
		t.Fatalf("count after prune = %d, want 1 (only 'fresh' remains)", n)
	}
}

func TestRestartPreservesState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dedup.db")
	clock := &fakeClock{t: time.Unix(1700000000, 0)}

	s1, err := Open(path, 24*time.Hour, clock.Now)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	_ = s1.MarkSeen(context.Background(), "hash-A")
	_ = s1.Close()

	s2, err := Open(path, 24*time.Hour, clock.Now)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s2.Close()
	skip, err := s2.SkipOrMark(context.Background(), "hash-A")
	if err != nil || !skip {
		t.Fatalf("after restart, fresh hash should still be skipped; skip=%v err=%v", skip, err)
	}
}
