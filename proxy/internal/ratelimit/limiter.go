// Package ratelimit provides per-tenant token-bucket rate limiting for the proxy.
//
// Each tenant gets one token bucket.  Tokens replenish at the configured RPM
// rate.  The default is 60 RPM per tenant (~1 request/second).  The global
// tenant (requests without X-Apprentice-Tenant) shares a single bucket.
package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rpm      int
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

func New(rpm int) *Limiter {
	if rpm <= 0 {
		rpm = 60
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rpm:     rpm,
	}
}

func (l *Limiter) Allow(tenantID string) bool {
	if tenantID == "" {
		tenantID = "global"
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b, exists := l.buckets[tenantID]
	if !exists {
		l.buckets[tenantID] = &bucket{
			tokens:     float64(l.rpm),
			lastRefill: time.Now(),
		}
		b = l.buckets[tenantID]
	}

	// Refill tokens based on elapsed time.
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	refill := elapsed * float64(l.rpm) / 60.0
	if refill > 0 {
		b.tokens += refill
		if b.tokens > float64(l.rpm) {
			b.tokens = float64(l.rpm)
		}
		b.lastRefill = now
	}

	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}

func (l *Limiter) Remaining(tenantID string) int {
	if tenantID == "" {
		tenantID = "global"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, exists := l.buckets[tenantID]
	if !exists {
		return l.rpm
	}
	// Refill first.
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	refill := elapsed * float64(l.rpm) / 60.0
	tokens := b.tokens + refill
	if tokens > float64(l.rpm) {
		tokens = float64(l.rpm)
	}
	return int(tokens)
}
