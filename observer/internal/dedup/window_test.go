package dedup

import (
	"testing"
	"time"
)

func TestWindow_DuplicateInsideWindowIsSeen(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	w := New(60*time.Second, clock.Now)

	if w.Seen("s1", "hashA") {
		t.Fatalf("first observation should be fresh")
	}
	clock.advance(30 * time.Second)
	if !w.Seen("s1", "hashA") {
		t.Fatalf("duplicate inside window should be flagged")
	}
}

func TestWindow_DuplicateAfterWindowIsFresh(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	w := New(60*time.Second, clock.Now)

	w.Seen("s1", "hashA")
	clock.advance(61 * time.Second)
	if w.Seen("s1", "hashA") {
		t.Fatalf("after window expired, observation should be treated as fresh")
	}
}

func TestWindow_DifferentSessionsDoNotCollide(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	w := New(60*time.Second, clock.Now)

	w.Seen("s1", "hashA")
	if w.Seen("s2", "hashA") {
		t.Fatalf("same hash in different session must not dedupe")
	}
}

func TestWindow_SweepEvictsExpired(t *testing.T) {
	clock := newFakeClock(time.Unix(1000, 0))
	w := New(60*time.Second, clock.Now)

	w.Seen("s1", "hashA")
	w.Seen("s2", "hashB")
	if got := w.Size(); got != 2 {
		t.Fatalf("size = %d, want 2", got)
	}
	clock.advance(120 * time.Second)
	// next Seen triggers a sweep
	w.Seen("s3", "hashC")
	if got := w.Size(); got != 1 {
		t.Fatalf("size after sweep = %d, want 1 (only the new one)", got)
	}
}

type fakeClock struct {
	t time.Time
}

func newFakeClock(t time.Time) *fakeClock         { return &fakeClock{t: t} }
func (f *fakeClock) Now() time.Time               { return f.t }
func (f *fakeClock) advance(d time.Duration)      { f.t = f.t.Add(d) }
