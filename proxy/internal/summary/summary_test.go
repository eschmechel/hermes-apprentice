package summary

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixtureLines() string {
	now := time.Now().UTC().Round(time.Second)
	lines := []map[string]any{
		{
			"time": now.Format(time.RFC3339), "msg": "request", "route_decision": "specialist",
			"pattern_id": "p1", "latency_ms": 100, "status": 200,
			"prompt_tokens": 100, "completion_tokens": 50,
			"estimated_cost_usd": 0, "cost_saved_usd": 0.001,
		},
		{
			"time": now.Add(1 * time.Minute).Format(time.RFC3339), "msg": "request", "route_decision": "specialist",
			"pattern_id": "p1", "latency_ms": 200, "status": 200,
			"prompt_tokens": 100, "completion_tokens": 50,
			"estimated_cost_usd": 0, "cost_saved_usd": 0.002,
		},
		{
			"time": now.Add(2 * time.Minute).Format(time.RFC3339), "msg": "request", "route_decision": "upstream",
			"pattern_id": "", "latency_ms": 500, "status": 200,
			"prompt_tokens": 100, "completion_tokens": 200,
			"estimated_cost_usd": 0.003, "cost_saved_usd": 0,
		},
		{
			"time": now.Add(3 * time.Minute).Format(time.RFC3339), "msg": "request", "route_decision": "fallback",
			"pattern_id": "p2", "latency_ms": 800, "status": 200,
			"prompt_tokens": 100, "completion_tokens": 100,
			"estimated_cost_usd": 0.005, "cost_saved_usd": 0,
		},
	}
	var sb strings.Builder
	for _, m := range lines {
		b, _ := json.Marshal(m)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestGenerate(t *testing.T) {
	r := strings.NewReader(fixtureLines())
	since := time.Now().UTC().Add(-1 * time.Hour)
	until := time.Now().UTC().Add(2 * time.Hour)

	report, err := Generate(r, since, until)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if report.GeneratedAt == "" {
		t.Error("expected generated_at")
	}
	if len(report.PerPattern) == 0 {
		t.Fatal("expected per_pattern entries")
	}

	// p1: 2 specialist calls
	var p1 *PatternResult
	for i := range report.PerPattern {
		if report.PerPattern[i].PatternID == "p1" {
			p1 = &report.PerPattern[i]
			break
		}
	}
	if p1 == nil {
		t.Fatal("expected p1 in per_pattern")
	}
	if p1.Volume != 2 {
		t.Errorf("p1 volume = %d, want 2", p1.Volume)
	}
	if p1.FallbackRate != 0 {
		t.Errorf("p1 fallback_rate = %f, want 0", p1.FallbackRate)
	}
	if p1.CostSavedUSD != 0.003 {
		t.Errorf("p1 cost_saved = %f, want 0.003", p1.CostSavedUSD)
	}

	// _unmatched: 1 upstream call
	var unmatched *PatternResult
	for i := range report.PerPattern {
		if report.PerPattern[i].PatternID == "_unmatched" {
			unmatched = &report.PerPattern[i]
			break
		}
	}
	if unmatched == nil {
		t.Fatal("expected _unmatched in per_pattern")
	}
	if unmatched.Volume != 1 {
		t.Errorf("_unmatched volume = %d, want 1", unmatched.Volume)
	}

	// p2: 1 fallback
	var p2 *PatternResult
	for i := range report.PerPattern {
		if report.PerPattern[i].PatternID == "p2" {
			p2 = &report.PerPattern[i]
			break
		}
	}
	if p2 == nil {
		t.Fatal("expected p2 in per_pattern")
	}
	if p2.Volume != 1 {
		t.Errorf("p2 volume = %d, want 1", p2.Volume)
	}
	if p2.FallbackRate != 1.0 {
		t.Errorf("p2 fallback_rate = %f, want 1.0", p2.FallbackRate)
	}

	// totals
	if report.Totals.Volume != 4 {
		t.Errorf("totals volume = %d, want 4", report.Totals.Volume)
	}
}

func TestGenerate_emptyInput(t *testing.T) {
	r := strings.NewReader("")
	since := time.Now().UTC().Add(-1 * time.Hour)
	until := time.Now().UTC().Add(1 * time.Hour)
	report, err := Generate(r, since, until)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(report.PerPattern) != 0 {
		t.Errorf("expected empty per_pattern, got %d entries", len(report.PerPattern))
	}
}

func TestGenerate_outsideWindow(t *testing.T) {
	lines := fixtureLines()
	r := strings.NewReader(lines)
	// window from 10 hours in the future — should match nothing
	since := time.Now().UTC().Add(10 * time.Hour)
	until := time.Now().UTC().Add(20 * time.Hour)
	report, err := Generate(r, since, until)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(report.PerPattern) != 0 {
		t.Errorf("expected 0 entries outside window, got %d", len(report.PerPattern))
	}
}

func TestGenerateAcceptsFloatLatency(t *testing.T) {
	// Regression: the parser used to declare latency_ms as int64, so a
	// re-emitted log pipeline that float-ified numeric fields produced
	// silently-empty reports. Float latency must aggregate identically to
	// integer latency.
	now := time.Now().UTC().Round(time.Second)
	lines := []map[string]any{
		{
			"time": now.Format(time.RFC3339), "msg": "request",
			"route_decision": "specialist", "pattern_id": "p1",
			"latency_ms": 1.5, "status": 200,
			"estimated_cost_usd": 0.0, "cost_saved_usd": 0.001,
		},
		{
			"time": now.Add(1 * time.Minute).Format(time.RFC3339), "msg": "request",
			"route_decision": "specialist", "pattern_id": "p1",
			"latency_ms": 2.5, "status": 200,
			"estimated_cost_usd": 0.0, "cost_saved_usd": 0.002,
		},
	}
	var sb strings.Builder
	for _, m := range lines {
		b, _ := json.Marshal(m)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	since := time.Now().UTC().Add(-1 * time.Hour)
	until := time.Now().UTC().Add(2 * time.Hour)
	report, err := Generate(strings.NewReader(sb.String()), since, until)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(report.PerPattern) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(report.PerPattern))
	}
	p := report.PerPattern[0]
	if p.PatternID != "p1" || p.Volume != 2 {
		t.Errorf("unexpected agg: %+v", p)
	}
	if p.AvgLatencyMs != 2.0 {
		t.Errorf("expected avg 2.0ms (preserving sub-ms precision), got %v", p.AvgLatencyMs)
	}
}

func TestResultIsValidJSON(t *testing.T) {
	r := strings.NewReader(fixtureLines())
	since := time.Now().UTC().Add(-1 * time.Hour)
	until := time.Now().UTC().Add(2 * time.Hour)
	report, err := Generate(r, since, until)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	// round-trip
	var check Report
	if err := json.Unmarshal(b, &check); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if check.Totals.Volume != report.Totals.Volume {
		t.Errorf("round-trip mismatch")
	}
}
