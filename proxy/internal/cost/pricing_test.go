package cost

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const epsilon = 1e-9

func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestDefaults(t *testing.T) {
	m := Defaults()
	if len(m) == 0 {
		t.Fatal("expected non-empty defaults")
	}
	for name, p := range m {
		if p.PromptPerMillionUSD < 0 || p.CompletionPerMillionUSD < 0 {
			t.Errorf("%s: negative pricing: %+v", name, p)
		}
	}
}

func TestComputeCost_known(t *testing.T) {
	p := New(nil)
	got := p.ComputeCost("openai/gpt-4o-mini", 1000, 500)
	// prompt: 1000/1e6 * 0.15 = 0.00015
	// completion: 500/1e6 * 0.60 = 0.00030
	// total = 0.00045
	want := 0.00045
	if !floatEqual(got, want) {
		t.Errorf("compute cost = %f, want %f", got, want)
	}
}

func TestComputeCost_unknown(t *testing.T) {
	p := New(nil)
	got := p.ComputeCost("bogus/model", 1000, 1000)
	if got != -1 {
		t.Errorf("expected -1 for unknown model, got %f", got)
	}
}

func TestLoadFile_missing(t *testing.T) {
	p, err := LoadFile(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := p.ComputeCost("openai/gpt-4o-mini", 1000, 0); !floatEqual(v, 0.00015) {
		t.Errorf("expected default pricing for known model, got %f", v)
	}
}

func TestLoadFile_valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.json")
	custom := map[string]ModelPricing{
		"my/custom": {0.10, 0.20},
	}
	b, _ := json.Marshal(custom)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := p.ComputeCost("my/custom", 1_000_000, 1_000_000)
	want := 0.30
	if !floatEqual(got, want) {
		t.Errorf("custom cost = %f, want %f", got, want)
	}
	// Defaults are still present (merged with custom)
	if v := p.ComputeCost("openai/gpt-4o-mini", 1000, 0); !floatEqual(v, 0.00015) {
		t.Errorf("expected default pricing for known model, got %f", v)
	}
}

func TestLookup(t *testing.T) {
	p := New(nil)
	pr, ok := p.Lookup("openai/gpt-4o")
	if !ok {
		t.Fatal("expected pricing for gpt-4o")
	}
	if !floatEqual(pr.PromptPerMillionUSD, 2.50) {
		t.Errorf("prompt price = %f", pr.PromptPerMillionUSD)
	}
	_, ok = p.Lookup("nonexistent")
	if ok {
		t.Error("expected no pricing for nonexistent model")
	}
}
