package canary

import (
	"path/filepath"
	"testing"
)

func fixedRand(values ...float64) func() float64 {
	var i int
	return func() float64 {
		if i >= len(values) {
			return 0
		}
		v := values[i]
		i++
		return v
	}
}

func TestNewManager(t *testing.T) {
	dir := t.TempDir()
	m, err := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestRegisterAddsPattern(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	if err := m.Register("p1"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r, ok := m.State("p1")
	if !ok {
		t.Fatal("expected pattern to exist after register")
	}
	if r.State != StateWarming {
		t.Fatalf("expected warming, got %s", r.State)
	}
	if r.Pct != DefaultConfig().StartPct {
		t.Fatalf("expected start pct %d, got %d", DefaultConfig().StartPct, r.Pct)
	}
}

func TestRegisterIdempotent(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	_ = m.Register("p1")
	err := m.Register("p1")
	if err != nil {
		t.Fatalf("second register should be no-op: %v", err)
	}
}

func TestDecisionUntrackedReturnsNormal(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	route, managed := m.Decision("unknown")
	if !route {
		t.Fatal("untracked pattern should route to specialist")
	}
	if managed {
		t.Fatal("untracked pattern should not be managed")
	}
}

func TestDecisionLive(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	_ = m.Register("p1")
	_ = m.SetState("p1", StateLive, 100)
	route, managed := m.Decision("p1")
	if !route {
		t.Fatal("live pattern should route to specialist")
	}
	if !managed {
		t.Fatal("live pattern should be managed")
	}
}

func TestDecisionBroken(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	_ = m.Register("p1")
	_ = m.SetState("p1", StateBroken, 0)
	route, managed := m.Decision("p1")
	if route {
		t.Fatal("broken pattern should NOT route to specialist")
	}
	if !managed {
		t.Fatal("broken pattern should be managed")
	}
}

func TestDecisionWarming(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), Config{StartPct: 50, StepPct: 10, StepRequests: 10, AgreementThresh: 0.8})
	_ = m.Register("p1")
	r, _ := m.State("p1")
	if r.Pct != 50 {
		t.Fatalf("expected 50, got %d", r.Pct)
	}
	m.randFn = fixedRand(0.3, 0.7, 0.9)
	cases := []struct {
		idx    int
		expect bool
	}{
		{0, true},  // 0.3 < 0.5 → specialist
		{1, false}, // 0.7 > 0.5 → upstream
		{2, false}, // 0.9 > 0.5 → upstream
	}
	for _, c := range cases {
		route, managed := m.Decision("p1")
		if !managed {
			t.Fatal("warming pattern should be managed")
		}
		if route != c.expect {
			t.Fatalf("case %d: expected route=%v, got %v", c.idx, c.expect, route)
		}
	}
}

func TestAdvanceBelowThresholdDemotes(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), Config{
		StartPct: 5, StepPct: 10, StepRequests: 3, AgreementThresh: 0.8,
	})
	_ = m.Register("p1")
	state, transitioned, err := m.Advance("p1", 0.5)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if transitioned {
		t.Fatal("expected no transition before reaching step count")
	}
	_ = state
	state, transitioned, err = m.Advance("p1", 0.3)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if transitioned {
		t.Fatal("expected no transition before reaching step count")
	}
	state, transitioned, err = m.Advance("p1", 0.4)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if !transitioned {
		t.Fatal("expected transition after 3 records")
	}
	if state != StateBroken {
		t.Fatalf("expected broken (avg=%f < 0.8), got %s", (0.5+0.3+0.4)/3, state)
	}
}

func TestAdvanceAboveThresholdPromotes(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), Config{
		StartPct: 5, StepPct: 10, StepRequests: 2, AgreementThresh: 0.8,
	})
	_ = m.Register("p1")
	state, transitioned, _ := m.Advance("p1", 0.9)
	state, transitioned, _ = m.Advance("p1", 0.95)
	if !transitioned {
		t.Fatal("expected transition after 2 records")
	}
	if state != StateWarming {
		t.Fatalf("expected warming with increased pct, got %s", state)
	}
	r, _ := m.State("p1")
	if r.Pct != 15 {
		t.Fatalf("expected pct 15 (5+10), got %d", r.Pct)
	}
}

func TestAdvanceToLive(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), Config{
		StartPct: 90, StepPct: 10, StepRequests: 1, AgreementThresh: 0.8,
	})
	_ = m.Register("p1")
	state, transitioned, _ := m.Advance("p1", 0.95)
	if !transitioned {
		t.Fatal("expected transition")
	}
	if state != StateLive {
		t.Fatalf("expected live at 100%%, got %s", state)
	}
	r, _ := m.State("p1")
	if r.Pct != 100 {
		t.Fatalf("expected pct 100, got %d", r.Pct)
	}
}

func TestCompareResponsesIdentical(t *testing.T) {
	body := `{"choices":[{"message":{"content":"hello world"}}]}`
	score := CompareResponses([]byte(body), []byte(body))
	if score != 1.0 {
		t.Fatalf("expected 1.0 for identical, got %f", score)
	}
}

func TestCompareResponsesDifferent(t *testing.T) {
	spec := `{"choices":[{"message":{"content":"hello world foo"}}]}`
	up := `{"choices":[{"message":{"content":"hello universe bar"}}]}`
	score := CompareResponses([]byte(spec), []byte(up))
	if score >= 1.0 || score == 0 {
		t.Fatalf("expected partial agreement (>0, <1), got %f", score)
	}
}

func TestCompareResponsesNoOverlap(t *testing.T) {
	spec := `{"choices":[{"message":{"content":"aaa bbb ccc"}}]}`
	up := `{"choices":[{"message":{"content":"xxx yyy zzz"}}]}`
	score := CompareResponses([]byte(spec), []byte(up))
	if score != 0 {
		t.Fatalf("expected 0 for no overlap, got %f", score)
	}
}

func TestCompareResponsesEmpty(t *testing.T) {
	score := CompareResponses(nil, nil)
	if score != 1.0 {
		t.Fatalf("expected 1.0 for both empty, got %f", score)
	}
}

func TestCompareResponsesOneEmpty(t *testing.T) {
	spec := `{"choices":[{"message":{"content":"hello"}}]}`
	score := CompareResponses([]byte(spec), nil)
	if score != 0.0 {
		t.Fatalf("expected 0.0 for one empty, got %f", score)
	}
}

func TestSendAlertOnlyOnBroken(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	_ = m.Register("p1")
	if alert := m.SendAlert("p1"); alert != "" {
		t.Fatal("expected no alert for warming pattern")
	}
	_ = m.SetState("p1", StateBroken, 0)
	alert := m.SendAlert("p1")
	if alert == "" {
		t.Fatal("expected alert for broken pattern")
	}
}

func TestPersistenceAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "canary.json")
	m1, _ := New(path, DefaultConfig())
	_ = m1.Register("p1")
	_ = m1.SetState("p1", StateLive, 100)

	m2, err := New(path, DefaultConfig())
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}
	r, ok := m2.State("p1")
	if !ok {
		t.Fatal("pattern should survive restart")
	}
	if r.State != StateLive {
		t.Fatalf("expected live after restart, got %s", r.State)
	}
	if r.Pct != 100 {
		t.Fatalf("expected pct 100 after restart, got %d", r.Pct)
	}
}

func TestListReturnsAll(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	_ = m.Register("p1")
	_ = m.Register("p2")
	list := m.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(list))
	}
}

func TestSetStateUnknown(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	err := m.SetState("nonexistent", StateLive, 100)
	if err == nil {
		t.Fatal("expected error for unknown pattern")
	}
}

func TestDecisionWarmingPctZero(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	m.randFn = fixedRand(0.0)
	_ = m.Register("p1")
	_ = m.SetState("p1", StateWarming, 0)
	route, managed := m.Decision("p1")
	if route {
		t.Fatal("expected route=false at 0%")
	}
	if !managed {
		t.Fatal("expected managed")
	}
}

func TestCompareResponsesJSONError(t *testing.T) {
	score := CompareResponses([]byte(`not json`), []byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	if score != 0.0 {
		t.Fatalf("expected 0 for unparseable, got %f", score)
	}
}

func TestAgreementRatio(t *testing.T) {
	if r := AgreementRatio(0.9, 0.8); r != 1.0 {
		t.Fatalf("expected 1.0, got %f", r)
	}
	if r := AgreementRatio(0.4, 0.8); r != 0.5 {
		t.Fatalf("expected 0.5, got %f", r)
	}
}

func TestRecordAgreementOnlyForWarming(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), DefaultConfig())
	_ = m.Register("p1")
	_ = m.SetState("p1", StateLive, 100)
	ready := m.RecordAgreement("p1", 0.9)
	if ready {
		t.Fatal("expected no ready for live pattern")
	}
	r, _ := m.State("p1")
	if r.RequestCount != 0 {
		t.Fatal("expected no request count increment for live pattern")
	}
}

func TestAdvancedStepsCorrectly(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(filepath.Join(dir, "canary.json"), Config{
		StartPct: 10, StepPct: 20, StepRequests: 2, AgreementThresh: 0.7,
	})
	_ = m.Register("p1")

	state, trans, _ := m.Advance("p1", 0.9)
	_ = state
	if trans {
		t.Fatal("expected no transition after 1 record")
	}

	state, trans, _ = m.Advance("p1", 0.8)
	if !trans {
		t.Fatal("expected transition after 2 records")
	}
	if state != StateWarming {
		t.Fatalf("expected warming, got %s", state)
	}
	r, _ := m.State("p1")
	if r.Pct != 30 {
		t.Fatalf("expected pct 30, got %d", r.Pct)
	}

	state, trans, _ = m.Advance("p1", 1.0)
	state, trans, _ = m.Advance("p1", 1.0)
	if !trans {
		t.Fatal("expected transition")
	}
	if state != StateWarming {
		t.Fatalf("expected warming at 50, got %s", state)
	}
	r, _ = m.State("p1")
	if r.Pct != 50 {
		t.Fatalf("expected pct 50 (30+20), got %d", r.Pct)
	}
}
