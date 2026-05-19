package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHWM_FreshDirStartsAtZero(t *testing.T) {
	dir := t.TempDir()
	h, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := h.Get(); got != 0 {
		t.Fatalf("Get on fresh dir = %d, want 0", got)
	}
}

func TestHWM_SetThenReload(t *testing.T) {
	dir := t.TempDir()
	h, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := h.Set(42); err != nil {
		t.Fatalf("Set: %v", err)
	}

	h2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if got := h2.Get(); got != 42 {
		t.Fatalf("reloaded Get = %d, want 42", got)
	}
}

func TestHWM_SetIgnoresLowerValues(t *testing.T) {
	dir := t.TempDir()
	h, _ := Load(dir)
	_ = h.Set(100)
	_ = h.Set(50)
	if got := h.Get(); got != 100 {
		t.Fatalf("Get after rewind attempt = %d, want 100", got)
	}
}

func TestHWM_AtomicWriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	h, _ := Load(dir)
	if err := h.Set(7); err != nil {
		t.Fatalf("Set: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
}
