package alias

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenNewStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestOpenLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")
	s1, _ := Open(path)
	_ = s1.Register("old-pattern", "merged-pattern")

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := s2.Resolve("old-pattern")
	if !ok || target != "merged-pattern" {
		t.Fatalf("expected merged-pattern, got %s (ok=%v)", target, ok)
	}
}

func TestOpenNonExistentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "aliases.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestRegisterAndResolve(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))

	_ = s.Register("a", "merged")
	target, ok := s.Resolve("a")
	if !ok || target != "merged" {
		t.Fatalf("expected merged, got %s", target)
	}
}

func TestResolveNonExistent(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	_, ok := s.Resolve("nonexistent")
	if ok {
		t.Fatal("expected false")
	}
}

func TestRegisterEmptyIDs(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	if err := s.Register("", "b"); err == nil {
		t.Fatal("expected error for empty alias_id")
	}
	if err := s.Register("a", ""); err == nil {
		t.Fatal("expected error for empty target_id")
	}
}

func TestRegisterSelfAlias(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	if err := s.Register("a", "a"); err == nil {
		t.Fatal("expected error for self-alias")
	}
}

func TestRemove(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	_ = s.Register("a", "merged")
	_ = s.Remove("a")
	_, ok := s.Resolve("a")
	if ok {
		t.Fatal("expected not found after remove")
	}
}

func TestList(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	_ = s.Register("a", "merged-a")
	_ = s.Register("b", "merged-b")
	entries := s.List()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestListEmpty(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	entries := s.List()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestPersistenceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")

	s, _ := Open(path)
	_ = s.Register("a", "merged-a")
	_ = s.Register("b", "merged-b")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty file")
	}
}
