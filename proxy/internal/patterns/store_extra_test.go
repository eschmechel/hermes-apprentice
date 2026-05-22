package patterns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListByTenantFiltersCorrectly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	global := Pattern{ID: "global-1", Centroid: []float32{1}, TenantID: ""}
	own := Pattern{ID: "own-1", Centroid: []float32{1}, TenantID: "t1"}
	other := Pattern{ID: "other-1", Centroid: []float32{1}, TenantID: "t2"}
	s.Upsert(global)
	s.Upsert(own)
	s.Upsert(other)

	view := s.ListByTenant("t1")
	if len(view) != 2 {
		t.Fatalf("expected 2 patterns (global + own), got %d", len(view))
	}
	ids := map[string]bool{}
	for _, p := range view {
		ids[p.ID] = true
	}
	if !ids["global-1"] || !ids["own-1"] {
		t.Errorf("expected global-1 and own-1, got %v", ids)
	}
}

func TestListByTenantOnlyOwn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, _ := Open(path)

	s.Upsert(Pattern{ID: "o1", Centroid: []float32{1}, TenantID: "t1"})
	s.Upsert(Pattern{ID: "o2", Centroid: []float32{1}, TenantID: "t2"})

	view := s.ListByTenant("t1")
	if len(view) != 1 || view[0].ID != "o1" {
		t.Fatalf("expected only o1, got %v", view)
	}
}

func TestBestMatchTenantScoped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, _ := Open(path)

	s.Upsert(Pattern{ID: "a", Centroid: normalize([]float32{1, 0}), TenantID: "t1"})
	s.Upsert(Pattern{ID: "b", Centroid: normalize([]float32{0.9, 0.1}), TenantID: "t2"})

	query := normalize([]float32{0.95, 0.05})
	match, ok := s.BestMatchTenant(query, 0.7, "t1")
	if !ok || match.Pattern.ID != "a" {
		t.Fatalf("expected match on 'a', got %v (ok=%v)", match.Pattern.ID, ok)
	}

	match2, ok2 := s.BestMatchTenant(query, 0.7, "t2")
	if !ok2 || match2.Pattern.ID != "b" {
		t.Fatalf("expected match on 'b', got %v (ok=%v)", match2.Pattern.ID, ok2)
	}
}

func TestBestMatchTenantNoMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, _ := Open(path)

	s.Upsert(Pattern{ID: "a", Centroid: normalize([]float32{1, 0}), TenantID: "t2"})
	query := normalize([]float32{1, 0})
	_, ok := s.BestMatchTenant(query, 0.7, "t1")
	if ok {
		t.Fatal("expected no match for tenant without patterns")
	}
}

func TestPatternTenantIDJSONRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, _ := Open(path)

	s.Upsert(Pattern{ID: "p1", Centroid: []float32{1}, TenantID: "tenant-a"})

	data, _ := os.ReadFile(path)
	var list []Pattern
	json.Unmarshal(data, &list)
	if len(list) != 1 || list[0].ID != "p1" || list[0].TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %+v", list)
	}
}

func TestPatternTenantIDEmptyOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, _ := Open(path)

	s.Upsert(Pattern{ID: "global", Centroid: []float32{1}, TenantID: ""})
	data, _ := os.ReadFile(path)

	var list []Pattern
	json.Unmarshal(data, &list)
	if list[0].TenantID != "" {
		t.Fatalf("expected empty tenant_id, got %q", list[0].TenantID)
	}
}

func TestListPreservesTenantID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	s, _ := Open(path)

	s.Upsert(Pattern{ID: "p1", Centroid: []float32{1}, TenantID: "t1"})
	list := s.List()
	if list[0].TenantID != "t1" {
		t.Fatalf("expected t1, got %q", list[0].TenantID)
	}
}

func TestBestMatchBelowThreshold(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "patterns.json"))
	s.Upsert(Pattern{ID: "p1", Centroid: normalize([]float32{1, 0})})
	query := normalize([]float32{0.5, 0.866})
	_, ok := s.BestMatch(query, 0.99)
	if ok {
		t.Fatal("expected no match below high threshold")
	}
}

func TestBestMatchNoPatterns(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "patterns.json"))
	_, ok := s.BestMatch(normalize([]float32{1}), 0.5)
	if ok {
		t.Fatal("expected no match in empty store")
	}
}

func TestUpsertMultipleSameID(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "patterns.json"))
	s.Upsert(Pattern{ID: "x", Centroid: []float32{1}})
	s.Upsert(Pattern{ID: "x", Centroid: normalize([]float32{0, 1})})
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 pattern after upsert with same ID, got %d", len(list))
	}
}

func TestCosineSameVector(t *testing.T) {
	v := normalize([]float32{2, 3, 4})
	result := cosine(v, v)
	if result < 0.999 || result > 1.001 {
		t.Errorf("expected 1.0 for identical vectors, got %f", result)
	}
}

func TestCosineOrthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	result := cosine(a, b)
	if result != 0 {
		t.Errorf("expected 0 for orthogonal, got %f", result)
	}
}

func TestCosineMismatchedDimensions(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0}
	result := cosine(a, b)
	if result != 0 {
		t.Errorf("expected 0 for mismatched dims, got %f", result)
	}
}
