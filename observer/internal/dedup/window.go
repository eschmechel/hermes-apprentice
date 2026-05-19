// Package dedup drops repeated (session_id, content) messages that arrive
// inside a rolling time window. This guards against Hermes-side retries that
// re-insert the same content within a few seconds — without it, the
// apprentice's training set would carry adversarial duplicates that bias
// quality filtering.
//
// Memory bound: entries auto-expire after Window, and a background sweep
// (triggered on every Seen call after the next sweep deadline) removes
// expired keys so the map stays roughly proportional to the volume in the
// last Window.
package dedup

import (
	"sync"
	"time"
)

type Window struct {
	window     time.Duration
	now        func() time.Time
	mu         sync.Mutex
	entries    map[string]time.Time
	nextSweep  time.Time
}

// New constructs a Window with the given retention. now is injected so tests
// can drive time deterministically; pass nil to use time.Now.
func New(window time.Duration, now func() time.Time) *Window {
	if now == nil {
		now = time.Now
	}
	return &Window{
		window:    window,
		now:       now,
		entries:   make(map[string]time.Time),
		nextSweep: now().Add(window),
	}
}

// Seen reports whether a message with the given session_id + content_hash was
// observed within the window. It always records the message either way so
// subsequent duplicates within the window will be dropped.
func (w *Window) Seen(sessionID, contentHash string) bool {
	key := sessionID + "|" + contentHash
	now := w.now()
	cutoff := now.Add(-w.window)

	w.mu.Lock()
	defer w.mu.Unlock()

	w.sweepLocked(now, cutoff)

	if ts, ok := w.entries[key]; ok && ts.After(cutoff) {
		// Refresh the timestamp so back-to-back duplicates extend the suppression.
		w.entries[key] = now
		return true
	}
	w.entries[key] = now
	return false
}

// Size returns the current entry count. Useful for tests + telemetry.
func (w *Window) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.entries)
}

func (w *Window) sweepLocked(now, cutoff time.Time) {
	if now.Before(w.nextSweep) {
		return
	}
	for k, ts := range w.entries {
		if ts.Before(cutoff) {
			delete(w.entries, k)
		}
	}
	w.nextSweep = now.Add(w.window)
}
