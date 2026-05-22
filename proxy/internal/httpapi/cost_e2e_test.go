package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func setupCostTest(t *testing.T, ledgerContent, proxyLogContent string) (*httptest.Server, string) {
	t.Helper()
	tmp := t.TempDir()

	stateDir := filepath.Join(tmp, "proxy")
	costDir := filepath.Join(tmp, "cost")
	os.MkdirAll(stateDir, 0o755)
	os.MkdirAll(costDir, 0o755)

	if ledgerContent != "" {
		os.WriteFile(filepath.Join(costDir, "ledger.jsonl"), []byte(ledgerContent), 0o644)
	}
	if proxyLogContent != "" {
		os.WriteFile(filepath.Join(stateDir, "proxy.log"), []byte(proxyLogContent), 0o644)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ch := newCostHandler(stateDir, costDir, logger, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/cost/roi", ch.handleROI)
	mux.HandleFunc("GET /api/cost/roi/{pattern_id}", ch.handleROI)
	mux.HandleFunc("GET /api/cost/usage", ch.handleUsage)
	mux.HandleFunc("GET /api/cost/latency", ch.handleLatency)

	return httptest.NewServer(mux), stateDir
}

func TestE2E_ROI_AllPatterns(t *testing.T) {
	ledger := `{"ts":"2026-05-20T10:00:00Z","pattern_id":"p1","job_id":"j1","train_seconds":3600,"train_cost_usd":0.4}
{"ts":"2026-05-20T11:00:00Z","pattern_id":"p1","job_id":"j2","train_seconds":1800,"train_cost_usd":0.2}
{"ts":"2026-05-20T12:00:00Z","pattern_id":"p2","job_id":"j3","train_seconds":3600,"train_cost_usd":0.4}
`
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.15,"latency_ms":100}
{"time":"2026-05-20T11:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.2,"latency_ms":120}
{"time":"2026-05-20T12:00:00Z","route_decision":"specialist","pattern_id":"p2","cost_saved_usd":0.1,"latency_ms":90}
{"time":"2026-05-20T13:00:00Z","route_decision":"upstream","cost_saved_usd":0,"latency_ms":500}
`
	ts, _ := setupCostTest(t, ledger, proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/roi")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var results []roiResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(results))
	}

	p1 := results[0]
	if p1.PatternID != "p1" {
		t.Errorf("expected p1, got %s", p1.PatternID)
	}
	if p1.TrainCost != 0.6 {
		t.Errorf("p1 train cost: expected 0.6, got %f", p1.TrainCost)
	}
	if p1.Saved != 0.35 {
		t.Errorf("p1 saved: expected 0.35, got %f", p1.Saved)
	}
	if p1.ROI != -0.25 {
		t.Errorf("p1 ROI: expected -0.25, got %f", p1.ROI)
	}
	if p1.BrokeEven {
		t.Error("p1 should not have broken even")
	}
	if p1.Runs != 2 {
		t.Errorf("p1 runs: expected 2, got %d", p1.Runs)
	}
}

func TestE2E_ROI_SinglePattern(t *testing.T) {
	ledger := `{"ts":"2026-05-20T10:00:00Z","pattern_id":"p1","job_id":"j1","train_seconds":3600,"train_cost_usd":0.4}
`
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.5,"latency_ms":100}
`
	ts, _ := setupCostTest(t, ledger, proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/roi/p1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result roiResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.PatternID != "p1" {
		t.Errorf("expected p1, got %s", result.PatternID)
	}
	if result.BrokeEven != true {
		t.Error("p1 should have broken even (saved 0.5 > cost 0.4)")
	}
}

func TestE2E_ROI_EmptyLedger(t *testing.T) {
	ts, _ := setupCostTest(t, "", "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/roi")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var results []roiResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}

	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestE2E_ROI_NonexistentPattern(t *testing.T) {
	ledger := `{"ts":"2026-05-20T10:00:00Z","pattern_id":"p1","job_id":"j1","train_seconds":3600,"train_cost_usd":0.4}
`
	ts, _ := setupCostTest(t, ledger, "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/roi/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent pattern, got %d", resp.StatusCode)
	}
}

func TestE2E_Usage_DailyBuckets(t *testing.T) {
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.01,"latency_ms":100}
{"time":"2026-05-20T14:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.02,"latency_ms":110}
{"time":"2026-05-21T09:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.03,"latency_ms":120}
`
	ts, _ := setupCostTest(t, "", proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/usage?pattern_id=p1&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var buckets []usageBucket
	if err := json.NewDecoder(resp.Body).Decode(&buckets); err != nil {
		t.Fatal(err)
	}

	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(buckets))
	}

	if buckets[0].Time != "2026-05-20" {
		t.Errorf("bucket 0 time: expected 2026-05-20, got %s", buckets[0].Time)
	}
	if buckets[0].Requests != 2 {
		t.Errorf("bucket 0 requests: expected 2, got %d", buckets[0].Requests)
	}
	if buckets[0].CostSaved != 0.03 {
		t.Errorf("bucket 0 cost_saved: expected 0.03, got %f", buckets[0].CostSaved)
	}

	if buckets[1].Time != "2026-05-21" {
		t.Errorf("bucket 1 time: expected 2026-05-21, got %s", buckets[1].Time)
	}
	if buckets[1].Requests != 1 {
		t.Errorf("bucket 1 requests: expected 1, got %d", buckets[1].Requests)
	}
}

func TestE2E_Usage_HourlyBuckets(t *testing.T) {
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.01,"latency_ms":100}
{"time":"2026-05-20T10:30:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.02,"latency_ms":110}
{"time":"2026-05-20T11:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.03,"latency_ms":120}
`
	ts, _ := setupCostTest(t, "", proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/usage?pattern_id=p1&bucket=hour")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var buckets []usageBucket
	if err := json.NewDecoder(resp.Body).Decode(&buckets); err != nil {
		t.Fatal(err)
	}

	if len(buckets) != 2 {
		t.Fatalf("expected 2 hourly buckets, got %d", len(buckets))
	}

	if buckets[0].Time != "2026-05-20T10" {
		t.Errorf("bucket 0 time: expected 2026-05-20T10, got %s", buckets[0].Time)
	}
	if buckets[0].Requests != 2 {
		t.Errorf("bucket 0 requests: expected 2, got %d", buckets[0].Requests)
	}
}

func TestE2E_Usage_AllPatterns(t *testing.T) {
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.01,"latency_ms":100}
{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p2","cost_saved_usd":0.02,"latency_ms":110}
`
	ts, _ := setupCostTest(t, "", proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/usage?bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var buckets []usageBucket
	if err := json.NewDecoder(resp.Body).Decode(&buckets); err != nil {
		t.Fatal(err)
	}

	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].Requests != 2 {
		t.Errorf("expected 2 requests (both patterns), got %d", buckets[0].Requests)
	}
	if buckets[0].CostSaved != 0.03 {
		t.Errorf("expected 0.03 cost saved, got %f", buckets[0].CostSaved)
	}
}

func TestE2E_Latency_Stats(t *testing.T) {
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.01,"latency_ms":100}
{"time":"2026-05-20T10:01:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.02,"latency_ms":200}
{"time":"2026-05-20T10:02:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.03,"latency_ms":150}
{"time":"2026-05-20T10:03:00Z","route_decision":"upstream","cost_saved_usd":0,"latency_ms":500}
{"time":"2026-05-20T10:04:00Z","route_decision":"upstream","cost_saved_usd":0,"latency_ms":700}
`
	ts, _ := setupCostTest(t, "", proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/latency")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var stats map[string]latencyStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}

	spec := stats["specialist"]
	if spec.Count != 3 {
		t.Errorf("specialist count: expected 3, got %d", spec.Count)
	}
	if spec.Avg != 150 {
		t.Errorf("specialist avg: expected 150, got %f", spec.Avg)
	}

	up := stats["upstream"]
	if up.Count != 2 {
		t.Errorf("upstream count: expected 2, got %d", up.Count)
	}
	if up.Avg != 600 {
		t.Errorf("upstream avg: expected 600, got %f", up.Avg)
	}
}

func TestE2E_Latency_Empty(t *testing.T) {
	ts, _ := setupCostTest(t, "", "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/latency")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var stats map[string]latencyStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}

	if stats["specialist"].Count != 0 {
		t.Errorf("expected 0 specialist requests, got %d", stats["specialist"].Count)
	}
	if stats["upstream"].Count != 0 {
		t.Errorf("expected 0 upstream requests, got %d", stats["upstream"].Count)
	}
}

func TestE2E_Dashboard_ServesHTML(t *testing.T) {
	srv := New(Config{
		Addr:     ":0",
		StateDir: t.TempDir(),
	})

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type, got %s", contentType)
	}

	body := w.Body.String()
	if len(body) < 1000 {
		t.Errorf("dashboard HTML seems too short: %d bytes", len(body))
	}
}

func TestE2E_ROI_MultipleRunsSamePattern(t *testing.T) {
	ledger := `{"ts":"2026-05-20T10:00:00Z","pattern_id":"p1","job_id":"j1","train_seconds":3600,"train_cost_usd":0.4}
{"ts":"2026-05-20T11:00:00Z","pattern_id":"p1","job_id":"j2","train_seconds":7200,"train_cost_usd":0.8}
{"ts":"2026-05-20T12:00:00Z","pattern_id":"p1","job_id":"j3","train_seconds":1800,"train_cost_usd":0.2}
`
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":1.5,"latency_ms":100}
`
	ts, _ := setupCostTest(t, ledger, proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/roi/p1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result roiResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.TrainCost != 1.4 {
		t.Errorf("train cost: expected 1.4 (0.4+0.8+0.2), got %f", result.TrainCost)
	}
	if result.Runs != 3 {
		t.Errorf("runs: expected 3, got %d", result.Runs)
	}
	if result.BrokeEven != true {
		t.Error("should have broken even (saved 1.5 > cost 1.4)")
	}
}

func TestE2E_Usage_FiltersNonSpecialist(t *testing.T) {
	proxyLog := `{"time":"2026-05-20T10:00:00Z","route_decision":"specialist","pattern_id":"p1","cost_saved_usd":0.01,"latency_ms":100}
{"time":"2026-05-20T10:01:00Z","route_decision":"upstream","cost_saved_usd":99,"latency_ms":500}
{"time":"2026-05-20T10:02:00Z","route_decision":"fallback","cost_saved_usd":99,"latency_ms":600}
`
	ts, _ := setupCostTest(t, "", proxyLog)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/cost/usage?bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var buckets []usageBucket
	if err := json.NewDecoder(resp.Body).Decode(&buckets); err != nil {
		t.Fatal(err)
	}

	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].Requests != 1 {
		t.Errorf("expected only 1 specialist request, got %d", buckets[0].Requests)
	}
	if buckets[0].CostSaved != 0.01 {
		t.Errorf("expected 0.01 cost saved, got %f", buckets[0].CostSaved)
	}
}

// ── registry endpoint e2e tests ──────────────────────────────────────────────

func setupRegistryTest(t *testing.T, patternID string, version int, scores string) *httptest.Server {
	t.Helper()
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "proxy")
	registryRoot := filepath.Join(tmp, "registry")
	os.MkdirAll(stateDir, 0o755)

	// Create registry manifest for the pattern
	versDir := filepath.Join(registryRoot, patternID, "v"+strconv.Itoa(version))
	os.MkdirAll(versDir, 0o755)
	manifest := `{"schema_version":1,"pattern_id":"` + patternID + `","version":` + strconv.Itoa(version) + `,"model_dir":"/some/path","scores":` + scores + `,"promoted_at":"2026-05-20T10:00:00Z","source_training_manifest":"/some/training_manifest.json"}`
	os.WriteFile(filepath.Join(versDir, "registry_manifest.json"), []byte(manifest), 0o644)

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg := Config{
		Addr:         ":0",
		StateDir:     stateDir,
		CostDir:      filepath.Join(tmp, "cost"),
		RegistryRoot: registryRoot,
		Logger:       logger,
	}
	os.MkdirAll(cfg.CostDir, 0o755)

	mux := http.NewServeMux()

	rh := newRegistryHandler(cfg.RegistryRoot, cfg.Logger)
	mux.HandleFunc("GET /api/registry/{pattern_id}/latest", rh.handleLatest)

	return httptest.NewServer(mux)
}

func TestE2E_Registry_LatestFound(t *testing.T) {
	ts := setupRegistryTest(t, "p1", 3, `{"delta_f1":0.15,"delta_exact_match":0.12}`)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/registry/p1/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result["pattern_id"] != "p1" {
		t.Errorf("expected p1, got %v", result["pattern_id"])
	}
	v := result["version"]
	if v == nil || int(v.(float64)) != 3 {
		t.Errorf("expected version 3, got %v", v)
	}
	if result["model_dir"] != "/some/path" {
		t.Errorf("expected /some/path, got %v", result["model_dir"])
	}
}

func TestE2E_Registry_NonexistentPattern(t *testing.T) {
	ts := setupRegistryTest(t, "p1", 1, `{"delta_f1":0.1}`)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/registry/nonexistent/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent pattern, got %d", resp.StatusCode)
	}
}

func TestE2E_Registry_PicksHighestVersion(t *testing.T) {
	// Creates v1 and v3 for same pattern; /latest should return v3
	tmp := t.TempDir()
	registryRoot := filepath.Join(tmp, "registry")
	stateDir := filepath.Join(tmp, "proxy")
	os.MkdirAll(stateDir, 0o755)

	patternID := "p2"
	for _, v := range []int{1, 3} {
		versDir := filepath.Join(registryRoot, patternID, "v"+strconv.Itoa(v))
		os.MkdirAll(versDir, 0o755)
		manifest := `{"pattern_id":"` + patternID + `","version":` + strconv.Itoa(v) + `,"model_dir":"/m/v` + strconv.Itoa(v) + `","scores":{}}`
		os.WriteFile(filepath.Join(versDir, "registry_manifest.json"), []byte(manifest), 0o644)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	_ = Config{StateDir: stateDir, RegistryRoot: registryRoot, Logger: logger}

	mux := http.NewServeMux()
	rh := newRegistryHandler(registryRoot, logger)
	mux.HandleFunc("GET /api/registry/{pattern_id}/latest", rh.handleLatest)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/registry/p2/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	v := result["version"]
	if v == nil || int(v.(float64)) != 3 {
		t.Errorf("expected latest version 3, got %v", v)
	}
}

