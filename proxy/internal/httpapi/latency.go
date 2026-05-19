package httpapi

import (
	"math"
	"sort"
	"sync"
	"time"
)

const maxLatencySamples = 1024

type latencyRing struct {
	mu    sync.Mutex
	data  []time.Duration
	pos   int
	fill  int
}

func newLatencyRing() *latencyRing {
	return &latencyRing{data: make([]time.Duration, maxLatencySamples)}
}

func (r *latencyRing) add(d time.Duration) {
	r.mu.Lock()
	r.data[r.pos] = d
	r.pos = (r.pos + 1) % maxLatencySamples
	if r.fill < maxLatencySamples {
		r.fill++
	}
	r.mu.Unlock()
}

func (r *latencyRing) percentiles() (p50, p99 time.Duration) {
	r.mu.Lock()
	n := r.fill
	if n == 0 {
		r.mu.Unlock()
		return 0, 0
	}
	sorted := make([]time.Duration, n)
	if n < maxLatencySamples {
		copy(sorted, r.data[:n])
	} else {
		for i := 0; i < n; i++ {
			sorted[i] = r.data[(r.pos+i)%maxLatencySamples]
		}
	}
	r.mu.Unlock()
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx50 := int(math.Ceil(float64(n)*0.5)) - 1
	if idx50 < 0 {
		idx50 = 0
	}
	p50 = sorted[idx50]

	idx99 := int(math.Ceil(float64(n)*0.99)) - 1
	if idx99 < 0 {
		idx99 = 0
	}
	if idx99 >= n {
		idx99 = n - 1
	}
	p99 = sorted[idx99]

	return
}

type LatencyTracker struct {
	specialist *latencyRing
	upstream   *latencyRing
}

func NewLatencyTracker() *LatencyTracker {
	return &LatencyTracker{
		specialist: newLatencyRing(),
		upstream:   newLatencyRing(),
	}
}

func (t *LatencyTracker) RecordSpecialist(d time.Duration) { t.specialist.add(d) }
func (t *LatencyTracker) RecordUpstream(d time.Duration)   { t.upstream.add(d) }

func (t *LatencyTracker) Stats() (specP50, specP99, upP50, upP99 time.Duration) {
	specP50, specP99 = t.specialist.percentiles()
	upP50, upP99 = t.upstream.percentiles()
	return
}
