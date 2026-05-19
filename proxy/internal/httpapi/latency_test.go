package httpapi

import (
	"testing"
	"time"
)

func TestLatencyRing_empty(t *testing.T) {
	r := newLatencyRing()
	p50, p99 := r.percentiles()
	if p50 != 0 || p99 != 0 {
		t.Errorf("empty ring: p50=%v p99=%v", p50, p99)
	}
}

func TestLatencyRing_percentiles(t *testing.T) {
	r := newLatencyRing()
	for _, d := range []time.Duration{
		1 * time.Millisecond,
		5 * time.Millisecond,
		10 * time.Millisecond,
		100 * time.Millisecond,
	} {
		r.add(d)
	}
	p50, p99 := r.percentiles()
	if p50 != 5*time.Millisecond {
		t.Errorf("p50=%v, want 5ms", p50)
	}
	if p99 != 100*time.Millisecond {
		t.Errorf("p99=%v, want 100ms", p99)
	}
}

func TestLatencyRing_wrap(t *testing.T) {
	r := newLatencyRing()
	for i := 0; i < maxLatencySamples+10; i++ {
		r.add(time.Duration(i) * time.Millisecond)
	}
	p50, p99 := r.percentiles()
	if p50 == 0 {
		t.Fatal("expected non-zero p50 after wrapping")
	}
	if p99 == 0 {
		t.Fatal("expected non-zero p99 after wrapping")
	}
}

func TestLatencyTracker(t *testing.T) {
	tr := NewLatencyTracker()
	tr.RecordSpecialist(50 * time.Millisecond)
	tr.RecordSpecialist(150 * time.Millisecond)
	tr.RecordUpstream(200 * time.Millisecond)
	tr.RecordUpstream(400 * time.Millisecond)

	sP50, sP99, uP50, uP99 := tr.Stats()
	if sP50 != 50*time.Millisecond || sP99 != 150*time.Millisecond {
		t.Errorf("specialist p50=%v p99=%v", sP50, sP99)
	}
	if uP50 != 200*time.Millisecond || uP99 != 400*time.Millisecond {
		t.Errorf("upstream p50=%v p99=%v", uP50, uP99)
	}
}
