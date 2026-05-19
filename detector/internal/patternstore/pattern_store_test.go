package patternstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSave_GeneratesUUIDWhenIDEmpty(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	id, err := s.Save(Manifest{Description: "extract emails", RecordCount: 25})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(id) < 32 {
		t.Fatalf("expected UUID id, got %q", id)
	}
}

func TestSave_UsesExplicitID(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	id, err := s.Save(Manifest{
		ID:          "my-pattern",
		Description: "summarize articles",
		RecordCount: 30,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != "my-pattern" {
		t.Fatalf("id = %q, want %q", id, "my-pattern")
	}
}

func TestSave_DefaultsStatusAndTimestamp(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	id, err := s.Save(Manifest{
		Description: "translate text",
		RecordCount: 20,
		Centroid:    []float32{1.0, 2.0, 3.0},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	m, err := s.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Status != StatusCandidate {
		t.Fatalf("status = %q, want candidate", m.Status)
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero")
	}
	if len(m.Centroid) != 3 {
		t.Fatalf("centroid len = %d, want 3", len(m.Centroid))
	}
}

func TestLoad_Missing(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	_, err := s.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent pattern")
	}
}

func TestList_Empty(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	patterns, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(patterns) != 0 {
		t.Fatalf("expected empty list, got %d", len(patterns))
	}
}

func TestList_ReturnsSavedPatterns(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	id1, _ := s.Save(Manifest{Description: "foo", RecordCount: 10})
	id2, _ := s.Save(Manifest{Description: "bar", RecordCount: 20})

	patterns, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(patterns) != 2 {
		t.Fatalf("want 2 patterns, got %d", len(patterns))
	}
	ids := map[string]bool{patterns[0].ID: true, patterns[1].ID: true}
	if !ids[id1] || !ids[id2] {
		t.Fatalf("List missing ids: have %v, want {%s, %s}", ids, id1, id2)
	}
}

func TestSetStatus(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	id, _ := s.Save(Manifest{Description: "rewrite text"})

	if err := s.SetStatus(id, StatusApproved); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	m, err := s.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Status != StatusApproved {
		t.Fatalf("status = %q, want approved", m.Status)
	}
}

func TestRestartPersistence(t *testing.T) {
	dir := t.TempDir()

	// Write with first store instance.
	s1 := New(dir)
	id, err := s1.Save(Manifest{
		Description: "persist across restart",
		RecordCount: 42,
		Centroid:    []float32{0.1, 0.2},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Re-open and load.
	s2 := New(dir)
	m, err := s2.Load(id)
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	if m.Description != "persist across restart" {
		t.Fatalf("description = %q", m.Description)
	}
	if m.RecordCount != 42 {
		t.Fatalf("record_count = %d", m.RecordCount)
	}
}

func TestOpen_CreatesDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "patterns")

	s, err := Open(base)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.baseDir != base {
		t.Fatalf("baseDir = %q, want %q", s.baseDir, base)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestList_HandlesNonDirectories(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	// Write a stray file that is not a directory.
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("hello"), 0o644)

	// Should not crash and should skip the file.
	patterns, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(patterns) != 0 {
		t.Fatalf("expected empty, got %d", len(patterns))
	}
}

func TestList_SkipsBadManifests(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	// Create a directory with a corrupt manifest.
	badDir := filepath.Join(dir, "bad-pattern")
	os.MkdirAll(badDir, 0o755)
	os.WriteFile(filepath.Join(badDir, "manifest.json"), []byte("{not json}"), 0o644)

	// Also save a valid one.
	s.Save(Manifest{Description: "valid", RecordCount: 5})

	patterns, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(patterns) != 1 {
		t.Fatalf("expected 1 valid pattern, got %d", len(patterns))
	}
	if patterns[0].Description != "valid" {
		t.Fatalf("got description %q", patterns[0].Description)
	}
}

func TestList_MissingDir(t *testing.T) {
	s := New("/tmp/does-not-exist-ever-12345")
	patterns, err := s.List()
	if err != nil {
		t.Fatalf("List missing dir should not error: %v", err)
	}
	if patterns == nil {
		t.Fatal("expected empty slice, got nil")
	}
}

func TestManifest_JSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ps, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	orig := Manifest{
		ID:          "a0000000-0000-0000-0000-000000000001",
		Description: "JSON round-trip test",
		Centroid:    []float32{1.5, 2.5},
		RecordCount: 99,
		Status:      StatusCandidate,
		CreatedAt:   time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	}
	id, err := ps.Save(orig)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id != orig.ID {
		t.Fatalf("id mismatch: %q", id)
	}

	loaded, err := ps.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Description != orig.Description {
		t.Fatalf("description mismatch")
	}
	if loaded.RecordCount != orig.RecordCount {
		t.Fatalf("record_count mismatch")
	}
	if loaded.Status != orig.Status {
		t.Fatalf("status mismatch")
	}
	if len(loaded.Centroid) != len(orig.Centroid) ||
		loaded.Centroid[0] != orig.Centroid[0] ||
		loaded.Centroid[1] != orig.Centroid[1] {
		t.Fatalf("centroid mismatch")
	}
}
