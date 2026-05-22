package httpapi

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type costHandler struct {
	stateDir string
	logger   *slog.Logger
}

func newCostHandler(stateDir string, logger *slog.Logger) *costHandler {
	return &costHandler{stateDir: stateDir, logger: logger}
}

func (ch *costHandler) ledgerPath() string {
	return filepath.Join(filepath.Dir(ch.stateDir), "cost", "ledger.jsonl")
}

func (ch *costHandler) proxyLogPath() string {
	return filepath.Join(ch.stateDir, "proxy.log")
}

// ── data types ──────────────────────────────────────────────────────────────

type ledgerEntry struct {
	PatternID    string  `json:"pattern_id"`
	TrainCostUSD float64 `json:"train_cost_usd"`
}

type proxyLogEntry struct {
	Time          string  `json:"time"`
	RouteDecision string  `json:"route_decision"`
	PatternID     string  `json:"pattern_id"`
	CostSavedUSD  float64 `json:"cost_saved_usd"`
	LatencyMs     float64 `json:"latency_ms"`
}

type roiResult struct {
	PatternID  string  `json:"pattern_id"`
	TrainCost  float64 `json:"train_cost"`
	Saved      float64 `json:"saved"`
	ROI        float64 `json:"roi"`
	BrokeEven  bool    `json:"broke_even"`
	Runs       int     `json:"runs"`
}

type usageBucket struct {
	Time      string  `json:"time"`
	Requests  int     `json:"requests"`
	CostSaved float64 `json:"cost_saved"`
}

type latencyStats struct {
	Count int     `json:"count"`
	Avg   float64 `json:"avg"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (ch *costHandler) handleROI(w http.ResponseWriter, r *http.Request) {
	patternID := r.PathValue("pattern_id")

	ledger := readLedger(ch.ledgerPath())
	proxy := readProxyLog(ch.proxyLogPath())

	results := computeROI(ledger, proxy, patternID)
	if patternID != "" && len(results) == 0 {
		results = []roiResult{{PatternID: patternID}}
	}

	writeJSON(w, http.StatusOK, results)
}

func (ch *costHandler) handleUsage(w http.ResponseWriter, r *http.Request) {
	patternID := r.URL.Query().Get("pattern_id")
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}

	proxy := readProxyLog(ch.proxyLogPath())
	buckets := bucketUsage(proxy, patternID, bucket)

	writeJSON(w, http.StatusOK, buckets)
}

func (ch *costHandler) handleLatency(w http.ResponseWriter, r *http.Request) {
	proxy := readProxyLog(ch.proxyLogPath())
	stats := computeLatencyStats(proxy)

	writeJSON(w, http.StatusOK, stats)
}

// ── ledger parsing ───────────────────────────────────────────────────────────

func readLedger(path string) []ledgerEntry {
	var entries []ledgerEntry
	fh, err := os.Open(path)
	if err != nil {
		return entries
	}
	defer fh.Close()
	dec := json.NewDecoder(fh)
	for dec.More() {
		var e ledgerEntry
		if err := dec.Decode(&e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// ── proxy log parsing ────────────────────────────────────────────────────────

func readProxyLog(path string) []proxyLogEntry {
	var entries []proxyLogEntry
	fh, err := os.Open(path)
	if err != nil {
		return entries
	}
	defer fh.Close()
	dec := json.NewDecoder(fh)
	for dec.More() {
		var e proxyLogEntry
		if err := dec.Decode(&e); err != nil {
			// slog JSON handler wraps attrs under "msg" etc; try raw
			continue
		}
		if e.RouteDecision == "specialist" {
			entries = append(entries, e)
		}
	}
	return entries
}

// ── ROI computation ──────────────────────────────────────────────────────────

func computeROI(ledger []ledgerEntry, proxy []proxyLogEntry, filterID string) []roiResult {
	type accum struct {
		trainCost  float64
		saved      float64
		runs       int
		earliestTS string
	}

	byPattern := map[string]*accum{}

	for _, e := range ledger {
		if filterID != "" && e.PatternID != filterID {
			continue
		}
		a := byPattern[e.PatternID]
		if a == nil {
			a = &accum{}
			byPattern[e.PatternID] = a
		}
		a.trainCost += e.TrainCostUSD
		a.runs++
	}

	for _, e := range proxy {
		if filterID != "" && e.PatternID != filterID {
			continue
		}
		a := byPattern[e.PatternID]
		if a == nil {
			a = &accum{}
			byPattern[e.PatternID] = a
		}
		a.saved += e.CostSavedUSD
		if a.earliestTS == "" || e.Time < a.earliestTS {
			a.earliestTS = e.Time
		}
	}

	results := make([]roiResult, 0, len(byPattern))
	for pid, a := range byPattern {
		brokeEven := a.trainCost <= 0 || a.saved >= a.trainCost
		results = append(results, roiResult{
			PatternID: pid,
			TrainCost: math.Round(a.trainCost*1e6) / 1e6,
			Saved:     math.Round(a.saved*1e6) / 1e6,
			ROI:       math.Round((a.saved-a.trainCost)*1e6) / 1e6,
			BrokeEven: brokeEven,
			Runs:      a.runs,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].PatternID < results[j].PatternID
	})

	if len(results) == 0 && filterID != "" {
		results = []roiResult{{PatternID: filterID}}
	}

	return results
}

// ── usage bucketing ──────────────────────────────────────────────────────────

func bucketUsage(proxy []proxyLogEntry, patternID, bucket string) []usageBucket {
	buckets := map[string]*usageBucket{}

	for _, e := range proxy {
		if patternID != "" && e.PatternID != patternID {
			continue
		}
		ts, err := time.Parse(time.RFC3339, e.Time)
		if err != nil {
			// Try simpler formats
			if ts, err = time.Parse("2006-01-02T15:04:05Z", e.Time); err != nil {
				continue
			}
		}
		var key string
		switch bucket {
		case "hour":
			key = ts.Format("2006-01-02T15")
		case "week":
			_, week := ts.ISOWeek()
			key = ts.Format("2006") + "-W" + padInt(week)
		default: // day
			key = ts.Format("2006-01-02")
		}
		b := buckets[key]
		if b == nil {
			b = &usageBucket{Time: key}
			buckets[key] = b
		}
		b.Requests++
		b.CostSaved = math.Round((b.CostSaved+e.CostSavedUSD)*1e6) / 1e6
	}

	result := make([]usageBucket, 0, len(buckets))
	for _, b := range buckets {
		result = append(result, *b)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time < result[j].Time
	})
	if result == nil {
		result = []usageBucket{}
	}
	return result
}

func padInt(n int) string {
	s := strings.Repeat("0", 2) + itoa(n)
	return s[len(s)-2:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ── latency stats ────────────────────────────────────────────────────────────

func computeLatencyStats(proxy []proxyLogEntry) map[string]latencyStats {
	var specialistLat, upstreamLat []float64

	for _, e := range proxy {
		if e.RouteDecision == "specialist" {
			specialistLat = append(specialistLat, e.LatencyMs)
		} else if e.RouteDecision == "upstream" || e.RouteDecision == "fallback" {
			upstreamLat = append(upstreamLat, e.LatencyMs)
		}
	}

	return map[string]latencyStats{
		"specialist": calcStats(specialistLat),
		"upstream":   calcStats(upstreamLat),
	}
}

func calcStats(vals []float64) latencyStats {
	if len(vals) == 0 {
		return latencyStats{}
	}
	sort.Float64s(vals)
	n := len(vals)
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return latencyStats{
		Count: n,
		Avg:   math.Round(sum/float64(n)*100) / 100,
		P50:   vals[n*50/100],
		P95:   vals[n*95/100],
		P99:   vals[n*99/100],
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
