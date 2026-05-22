package alias

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")
	os.WriteFile(path, []byte("{bad json"), 0o644)
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestOpenInvalidPermissions(t *testing.T) {
	dir := t.TempDir()
	noPermDir := filepath.Join(dir, "noperm")
	os.Mkdir(noPermDir, 0o000)
	defer os.Chmod(noPermDir, 0o755)
	path := filepath.Join(noPermDir, "sub", "aliases.json")
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected permission error")
	}
}

func TestRegisterOverwrites(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	_ = s.Register("a", "first")
	_ = s.Register("a", "second")
	target, ok := s.Resolve("a")
	if !ok || target != "second" {
		t.Fatalf("expected second, got %s", target)
	}
}

func TestRemoveNonExistent(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	err := s.Remove("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveConcurrent(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	_ = s.Register("a", "target-a")

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			target, ok := s.Resolve("a")
			if !ok || target != "target-a" {
				t.Errorf("concurrent resolve failed: ok=%v target=%s", ok, target)
			}
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestPersistenceRoundtripFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")

	s, _ := Open(path)
	if err := s.Register("x", "y"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty file")
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := s2.Resolve("x")
	if !ok || target != "y" {
		t.Fatalf("expected y, got %s (ok=%v)", target, ok)
	}
}

func TestListAfterRemove(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	_ = s.Register("a", "ta")
	_ = s.Register("b", "tb")
	_ = s.Remove("a")
	entries := s.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d", len(entries))
	}
	if entries[0].AliasID != "b" {
		t.Errorf("expected b, got %s", entries[0].AliasID)
	}
}

func TestOpenReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aliases.json")
	os.Mkdir(dir, 0o755)
	os.WriteFile(path, nil, 0o000)
	os.Chmod(path, 0o000)
	defer os.Chmod(path, 0o644)

	s, err := Open(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected permission error opening unreadable file, got %v", err)
	}
	_ = s
}
