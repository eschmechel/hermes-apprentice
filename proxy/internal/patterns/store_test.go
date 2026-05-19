package patterns

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreUpsertReloadAndMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// 4-dim toy centroid, normalized
	centroid := normalize([]float32{1, 0, 0, 0})
	p := Pattern{
		ID:            "abc-123",
		Description:   "email extraction",
		Centroid:      centroid,
		SpecialistURL: "http://localhost:8000",
	}
	if err := s.Upsert(p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Re-open: pattern should still be there
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	got := s2.List()
	if len(got) != 1 || got[0].ID != "abc-123" {
		t.Fatalf("expected one pattern after reload, got %+v", got)
	}

	// Near match (cos = 1.0)
	m, ok := s2.BestMatch(centroid, 0.78)
	if !ok || m.Pattern.ID != "abc-123" || m.Similarity < 0.99 {
		t.Fatalf("BestMatch identity: ok=%v sim=%v id=%v", ok, m.Similarity, m.Pattern.ID)
	}

	// Below threshold
	far := normalize([]float32{0, 1, 0, 0})
	if _, ok := s2.BestMatch(far, 0.78); ok {
		t.Fatalf("BestMatch orthogonal: expected no match")
	}
}

func TestStoreUpsertRejectsBadInput(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "patterns.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, bad := range []Pattern{
		{ID: "", Centroid: []float32{1}, SpecialistURL: "http://x"},
		{ID: "x", Centroid: nil, SpecialistURL: "http://x"},
		{ID: "x", Centroid: []float32{1}, SpecialistURL: ""},
	} {
		if err := s.Upsert(bad); err == nil {
			t.Fatalf("expected error for bad pattern %+v", bad)
		}
	}
}

func TestStoreSurvivesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "patterns.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected parent dir created: %v", err)
	}
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(sum))
	if n == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}
