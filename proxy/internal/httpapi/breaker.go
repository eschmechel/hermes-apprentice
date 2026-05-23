package httpapi

import (
	"sync"
	"time"
)

// CircuitBreaker is a per-pattern failure breaker on the specialist path. After
// `threshold` consecutive specialist failures (errors, bad responses, failed
// output validation) it "opens" for `cooldown`: matched requests skip the
// specialist and fall through to upstream instead of hammering a broken model.
// After the cooldown one request is allowed through (half-open probe); its
// outcome closes or re-opens the breaker.
//
// In-memory and per-process by design — a breaker tripping is a fast local
// reaction; durable demotion is the shadow-diff consumer's job (W7 orchestrator).
type CircuitBreaker struct {
	mu        sync.Mutex
	states    map[string]*breakerState
	threshold int
	cooldown  time.Duration
	now       func() time.Time
}

type breakerState struct {
	failures  int
	openUntil time.Time
}

// NewCircuitBreaker returns a breaker that opens after `threshold` consecutive
// failures and stays open for `cooldown`. threshold<=0 disables it (always allow).
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		states:    make(map[string]*breakerState),
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
	}
}

// Allow reports whether a request for patternID may try the specialist.
func (b *CircuitBreaker) Allow(patternID string) bool {
	if b == nil || b.threshold <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[patternID]
	if st == nil {
		return true
	}
	if st.openUntil.IsZero() {
		return true
	}
	// Open until the cooldown elapses; then allow a single half-open probe.
	return !b.now().Before(st.openUntil)
}

// RecordSuccess closes the breaker for patternID.
func (b *CircuitBreaker) RecordSuccess(patternID string) {
	if b == nil || b.threshold <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.states, patternID)
}

// RecordFailure increments the failure count; on reaching the threshold the
// breaker opens for the cooldown window.
func (b *CircuitBreaker) RecordFailure(patternID string) {
	if b == nil || b.threshold <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[patternID]
	if st == nil {
		st = &breakerState{}
		b.states[patternID] = st
	}
	st.failures++
	if st.failures >= b.threshold {
		st.openUntil = b.now().Add(b.cooldown)
	}
}

// State reports "closed", "open", or "half-open" for observability/tests.
func (b *CircuitBreaker) State(patternID string) string {
	if b == nil || b.threshold <= 0 {
		return "closed"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[patternID]
	if st == nil || st.openUntil.IsZero() {
		return "closed"
	}
	if b.now().Before(st.openUntil) {
		return "open"
	}
	return "half-open"
}
