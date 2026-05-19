package versioned

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeGzipJSONL(t *testing.T, dir, name string, lines int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gw := gzip.NewWriter(f)
	enc := json.NewEncoder(gw)
	for i := 0; i < lines; i++ {
		_ = enc.Encode(map[string]string{"test": "data"})
	}
	gw.Close()
	f.Close()
	return path
}

func TestSaveAndLoad(t *testing.T) {
	base := t.TempDir()
	workDir := t.TempDir()
	_ = makeGzipJSONL(t, workDir, "train.jsonl.gz", 80)
	_ = makeGzipJSONL(t, workDir, "val.jsonl.gz", 10)
	_ = makeGzipJSONL(t, workDir, "test.jsonl.gz", 10)

	v, verDir, err := Save(base, "pat-1", workDir, 80, 20, 80, 10, 10)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}

	m, err := Load(verDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Version != 1 {
		t.Fatalf("manifest.Version = %d", m.Version)
	}
	if m.PatternID != "pat-1" {
		t.Fatalf("manifest.PatternID = %q", m.PatternID)
	}
	if m.TrainCount != 80 {
		t.Fatalf("train = %d", m.TrainCount)
	}
	if m.ValCount != 10 {
		t.Fatalf("val = %d", m.ValCount)
	}
	if m.TestCount != 10 {
		t.Fatalf("test = %d", m.TestCount)
	}
	if m.TotalCount != 100 {
		t.Fatalf("total = %d", m.TotalCount)
	}
	if m.OriginalCount != 80 {
		t.Fatalf("original = %d", m.OriginalCount)
	}
	if m.AugmentationCount != 20 {
		t.Fatalf("augmentation = %d", m.AugmentationCount)
	}
	if m.SHA256 == "" {
		t.Fatal("SHA256 is empty")
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero")
	}

	// Verify files exist in version dir.
	for _, fn := range []string{"train.jsonl.gz", "val.jsonl.gz", "test.jsonl.gz", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(verDir, fn)); err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
	}
}

func TestSave_IncrementsVersion(t *testing.T) {
	base := t.TempDir()
	workDir := t.TempDir()
	_ = makeGzipJSONL(t, workDir, "train.jsonl.gz", 5)
	_ = makeGzipJSONL(t, workDir, "val.jsonl.gz", 1)
	_ = makeGzipJSONL(t, workDir, "test.jsonl.gz", 1)

	v1, _, err := Save(base, "pat-1", workDir, 5, 0, 5, 0, 0)
	if err != nil || v1 != 1 {
		t.Fatalf("Save 1: v=%d err=%v", v1, err)
	}
	v2, _, err := Save(base, "pat-1", workDir, 5, 0, 5, 0, 0)
	if err != nil || v2 != 2 {
		t.Fatalf("Save 2: v=%d err=%v", v2, err)
	}
	v3, _, err := Save(base, "pat-1", workDir, 5, 0, 5, 0, 0)
	if err != nil || v3 != 3 {
		t.Fatalf("Save 3: v=%d err=%v", v3, err)
	}
}

func TestPruneKeep(t *testing.T) {
	base := t.TempDir()
	for i := 0; i < 5; i++ {
		workDir := t.TempDir()
		_ = makeGzipJSONL(t, workDir, "train.jsonl.gz", 5)
		_ = makeGzipJSONL(t, workDir, "val.jsonl.gz", 0)
		_ = makeGzipJSONL(t, workDir, "test.jsonl.gz", 0)
		_, _, _ = Save(base, "pat-1", workDir, 5, 0, 5, 0, 0)
	}

	if err := PruneKeep(filepath.Join(base, "pat-1"), 2); err != nil {
		t.Fatalf("PruneKeep: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(base, "pat-1"))
	var vers []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			vers = append(vers, e.Name())
		}
	}
	if len(vers) != 2 {
		t.Fatalf("remaining versions = %d, want 2: %v", len(vers), vers)
	}
	if vers[0] != "v4" || vers[1] != "v5" {
		t.Fatalf("expected v4 and v5, got %v", vers)
	}
}

func TestPruneOlderThan(t *testing.T) {
	base := t.TempDir()

	saveOne := func() {
		workDir := t.TempDir()
		_ = makeGzipJSONL(t, workDir, "train.jsonl.gz", 5)
		_ = makeGzipJSONL(t, workDir, "val.jsonl.gz", 1)
		_ = makeGzipJSONL(t, workDir, "test.jsonl.gz", 1)
		v, _, err := Save(base, "pat-1", workDir, 5, 0, 5, 1, 1)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if v < 1 {
			t.Fatalf("version = %d", v)
		}
	}
	saveOne()
	saveOne()

	// Prune older than 1 hour — everything is fresh, nothing removed.
	if err := PruneOlderThan(filepath.Join(base, "pat-1"), 1*time.Hour); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(base, "pat-1"))
	var vers []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			vers = append(vers, e.Name())
		}
	}
	if len(vers) != 2 {
		t.Fatalf("fresh cutoff should not prune; got %v", vers)
	}
}

func TestPruneOlderThan_RemovesOld(t *testing.T) {
	base := t.TempDir()
	workDir := t.TempDir()
	_ = makeGzipJSONL(t, workDir, "train.jsonl.gz", 5)
	_ = makeGzipJSONL(t, workDir, "val.jsonl.gz", 1)
	_ = makeGzipJSONL(t, workDir, "test.jsonl.gz", 1)

	_, verDir, err := Save(base, "pat-1", workDir, 5, 0, 5, 0, 0)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Manually backdate the manifest.
	m, _ := Load(verDir)
	m.CreatedAt = time.Now().UTC().Add(-48 * time.Hour)
	data, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(filepath.Join(verDir, "manifest.json"), data, 0o644)

	if err := PruneOlderThan(filepath.Join(base, "pat-1"), 24*time.Hour); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(base, "pat-1"))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			t.Fatal("old version should be pruned")
		}
	}
}

func TestPrune_NonexistentDir(t *testing.T) {
	if err := PruneKeep(filepath.Join(t.TempDir(), "nonexistent"), 2); err != nil {
		t.Fatalf("prune non-existent: %v", err)
	}
}

func TestDecompress(t *testing.T) {
	dir := t.TempDir()
	src := makeGzipJSONL(t, dir, "test.jsonl.gz", 3)
	dst := filepath.Join(dir, "test.jsonl")

	if err := Decompress(src, dst); err != nil {
		t.Fatalf("Decompress: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read decompressed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("decompressed lines = %d, want 3", len(lines))
	}
}

func TestDecompress_AutoName(t *testing.T) {
	dir := t.TempDir()
	src := makeGzipJSONL(t, dir, "test.jsonl.gz", 1)

	if err := Decompress(src, ""); err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test.jsonl")); err != nil {
		t.Fatalf("auto-named output missing: %v", err)
	}
}

func TestNextVersion_Empty(t *testing.T) {
	v, err := nextVersion(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("nextVersion: %v", err)
	}
	if v != 1 {
		t.Fatalf("v = %d, want 1", v)
	}
}
