// Package summary aggregates per-request proxy JSON log lines into a
// per-pattern report suitable for weekly cost/latency reviews.
package summary

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type logEntry struct {
	Time          string   `json:"time"`
	Msg           string   `json:"msg"`
	RouteDecision string   `json:"route_decision"`
	PatternID     string   `json:"pattern_id"`
	LatencyMs     int64    `json:"latency_ms"`
	Status        int      `json:"status"`
	CostUSD       *float64 `json:"estimated_cost_usd"`
	CostSavedUSD  float64  `json:"cost_saved_usd"`
}

type patternAgg struct {
	volume    int
	latencies []int64
	totalCost float64
	costSaved float64
	fallbacks int
}

type PatternResult struct {
	PatternID    string  `json:"pattern_id"`
	Volume       int     `json:"volume"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	P50Ms        float64 `json:"p50_ms"`
	P99Ms        float64 `json:"p99_ms"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	CostSavedUSD float64 `json:"cost_saved_usd"`
	FallbackRate float64 `json:"fallback_rate"`
}

type Report struct {
	GeneratedAt string          `json:"generated_at"`
	WindowStart string          `json:"window_start"`
	WindowEnd   string          `json:"window_end"`
	PerPattern  []PatternResult `json:"per_pattern"`
	Totals      PatternResult   `json:"totals"`
}

func Generate(r io.Reader, since, until time.Time) (*Report, error) {
	aggs := make(map[string]*patternAgg)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 1<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry logEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Msg != "request" {
			continue
		}
		t, err := time.Parse(time.RFC3339, entry.Time)
		if err != nil {
			continue
		}
		if t.Before(since) || !t.Before(until) {
			continue
		}

		pid := entry.PatternID
		if pid == "" {
			pid = "_unmatched"
		}
		agg, ok := aggs[pid]
		if !ok {
			agg = &patternAgg{}
			aggs[pid] = agg
		}
		agg.volume++
		agg.latencies = append(agg.latencies, entry.LatencyMs)
		if entry.CostUSD != nil {
			agg.totalCost += *entry.CostUSD
		}
		agg.costSaved += entry.CostSavedUSD
		if entry.RouteDecision == "fallback" {
			agg.fallbacks++
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading log: %w", err)
	}

	keys := make([]string, 0, len(aggs))
	for k := range aggs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	perPattern := make([]PatternResult, 0, len(keys))
	var totalVol, totalFallbacks int
	var totalCost, totalSaved float64
	var allLatencies []int64

	for _, pid := range keys {
		agg := aggs[pid]
		pr := buildResult(pid, agg)
		perPattern = append(perPattern, pr)
		totalVol += agg.volume
		totalFallbacks += agg.fallbacks
		totalCost += agg.totalCost
		totalSaved += agg.costSaved
		allLatencies = append(allLatencies, agg.latencies...)
	}

	totals := buildResult("_total", &patternAgg{
		volume:    totalVol,
		latencies: allLatencies,
		totalCost: totalCost,
		costSaved: totalSaved,
		fallbacks: totalFallbacks,
	})

	return &Report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		WindowStart: since.Format(time.RFC3339),
		WindowEnd:   until.Format(time.RFC3339),
		PerPattern:  perPattern,
		Totals:      totals,
	}, nil
}

func buildResult(patternID string, agg *patternAgg) PatternResult {
	pr := PatternResult{
		PatternID:    patternID,
		Volume:       agg.volume,
		TotalCostUSD: agg.totalCost,
		CostSavedUSD: agg.costSaved,
	}
	if agg.volume == 0 {
		return pr
	}

	var sumMs int64
	for _, l := range agg.latencies {
		sumMs += l
	}
	pr.AvgLatencyMs = float64(sumMs) / float64(len(agg.latencies))

	sorted := make([]int64, len(agg.latencies))
	copy(sorted, agg.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := float64(len(sorted))
	p50Idx := int(n * 0.50)
	p99Idx := int(n * 0.99)
	if p50Idx >= len(sorted) {
		p50Idx = len(sorted) - 1
	}
	if p99Idx >= len(sorted) {
		p99Idx = len(sorted) - 1
	}
	pr.P50Ms = float64(sorted[p50Idx])
	pr.P99Ms = float64(sorted[p99Idx])

	pr.FallbackRate = float64(agg.fallbacks) / float64(agg.volume)

	return pr
}

func OpenLogs(path string) (io.ReadCloser, error) {
	if path == "" || path == "-" {
		return io.NopCloser(os.Stdin), nil
	}

	matches, err := filepath.Glob(path)
	if err != nil {
		return nil, fmt.Errorf("glob pattern: %w", err)
	}
	if len(matches) == 0 {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		return f, nil
	}

	sort.Strings(matches)
	readers := make([]io.Reader, 0, len(matches))
	for _, fname := range matches {
		f, err := os.Open(fname)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", fname, err)
		}
		readers = append(readers, f)
	}
	return &multiFile{io.MultiReader(readers...), matches}, nil
}

type multiFile struct {
	io.Reader
	files []string
}

func (m *multiFile) Close() error {
	return nil
}
