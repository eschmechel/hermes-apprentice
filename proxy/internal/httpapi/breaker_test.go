package httpapi

import (
	"testing"
	"time"
)

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewCircuitBreaker(3, 10*time.Second)
	b.now = func() time.Time { return now }

	if !b.Allow("p") {
		t.Fatal("fresh breaker should allow")
	}
	b.RecordFailure("p")
	b.RecordFailure("p")
	if b.State("p") != "closed" {
		t.Fatalf("below threshold should stay closed, got %s", b.State("p"))
	}
	b.RecordFailure("p") // 3rd -> open
	if b.State("p") != "open" || b.Allow("p") {
		t.Fatalf("expected open+deny, state=%s allow=%v", b.State("p"), b.Allow("p"))
	}
}

func TestBreaker_HalfOpenThenClose(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewCircuitBreaker(1, 10*time.Second)
	b.now = func() time.Time { return now }

	b.RecordFailure("p") // opens immediately (threshold 1)
	if b.Allow("p") {
		t.Fatal("should be open")
	}
	now = now.Add(11 * time.Second) // cooldown elapsed
	if !b.Allow("p") || b.State("p") != "half-open" {
		t.Fatalf("expected half-open probe allowed, state=%s", b.State("p"))
	}
	b.RecordSuccess("p")
	if !b.Allow("p") || b.State("p") != "closed" {
		t.Fatalf("success should close, state=%s", b.State("p"))
	}
}

func TestBreaker_SuccessResetsFailures(t *testing.T) {
	b := NewCircuitBreaker(3, time.Second)
	b.RecordFailure("p")
	b.RecordFailure("p")
	b.RecordSuccess("p") // reset
	b.RecordFailure("p")
	b.RecordFailure("p")
	if b.State("p") != "closed" {
		t.Fatalf("two failures after reset must not open, state=%s", b.State("p"))
	}
}

func TestBreaker_DisabledWhenThresholdZero(t *testing.T) {
	b := NewCircuitBreaker(0, time.Second)
	b.RecordFailure("p")
	b.RecordFailure("p")
	if !b.Allow("p") || b.State("p") != "closed" {
		t.Fatal("threshold<=0 disables the breaker")
	}
}

func TestBreaker_NilSafe(t *testing.T) {
	var b *CircuitBreaker
	if !b.Allow("p") {
		t.Fatal("nil breaker must allow")
	}
	b.RecordFailure("p") // must not panic
	b.RecordSuccess("p")
}
