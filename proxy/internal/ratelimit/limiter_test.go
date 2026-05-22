package ratelimit

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	l := New(60)
	if l.rpm != 60 {
		t.Fatalf("expected rpm 60, got %d", l.rpm)
	}
}

func TestDefaultRPM(t *testing.T) {
	l := New(0)
	if l.rpm != 60 {
		t.Fatalf("expected default rpm 60, got %d", l.rpm)
	}
}

func TestAllowFirstRequest(t *testing.T) {
	l := New(60)
	if !l.Allow("tenant-1") {
		t.Fatal("first request should be allowed")
	}
}

func TestAllowExhaustsTokens(t *testing.T) {
	l := New(5)
	for i := 0; i < 5; i++ {
		if !l.Allow("tenant-1") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow("tenant-1") {
		t.Fatal("6th request should be denied")
	}
}

func TestAllowRefill(t *testing.T) {
	l := New(60)
	for i := 0; i < 60; i++ {
		l.Allow("tenant-1")
	}
	if l.Allow("tenant-1") {
		t.Fatal("61st request should be denied")
	}

	l.mu.Lock()
	b := l.buckets["tenant-1"]
	b.lastRefill = time.Now().Add(-2 * time.Second)
	l.mu.Unlock()

	if !l.Allow("tenant-1") {
		t.Fatal("request after refill should be allowed")
	}
}

func TestSeparateBuckets(t *testing.T) {
	l := New(5)
	for i := 0; i < 5; i++ {
		l.Allow("tenant-1")
	}
	if l.Allow("tenant-1") {
		t.Fatal("tenant-1 should be exhausted")
	}
	if !l.Allow("tenant-2") {
		t.Fatal("tenant-2 should have its own bucket")
	}
}

func TestGlobalTenant(t *testing.T) {
	l := New(5)
	for i := 0; i < 5; i++ {
		l.Allow("global")
	}
	if l.Allow("") {
		t.Fatal("empty ID should use global bucket which is exhausted")
	}
}

func TestRemaining(t *testing.T) {
	l := New(10)
	if r := l.Remaining("t1"); r != 10 {
		t.Fatalf("expected 10 remaining, got %d", r)
	}
	for i := 0; i < 4; i++ {
		l.Allow("t1")
	}
	if r := l.Remaining("t1"); r != 6 {
		t.Fatalf("expected 6 remaining, got %d", r)
	}
}
