// Package patterns is the proxy's in-memory + on-disk registry of routable
// patterns.  Patterns are pushed in by the detector (POST /patterns) after
// operator approval and persist to ~/.apprentice/proxy/patterns.json so the
// proxy can resume routing after a restart without needing the detector
// online.
package patterns

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// Pattern is one routable entry: a centroid (BGE-small 384-dim) plus the
// specialist endpoint to use when an incoming request's embedding cosines
// above the proxy's match threshold against the centroid.
type Pattern struct {
	ID            string    `json:"id"`
	Description   string    `json:"description"`
	Centroid      []float32 `json:"centroid"`
	SpecialistURL string    `json:"specialist_url"`
}

// Match is the result of matching an embedding against the store.
type Match struct {
	Pattern    Pattern
	Similarity float32
}

// Store is a concurrent in-memory registry backed by a JSON file.
type Store struct {
	mu   sync.RWMutex
	path string
	data map[string]Pattern
}

// Open loads patterns from path (if present) and returns a Store ready to be
// queried and mutated.  The parent directory of path is created if missing.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("patterns: mkdir %s: %w", filepath.Dir(path), err)
	}
	s := &Store{path: path, data: make(map[string]Pattern)}

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("patterns: open %s: %w", path, err)
	}
	defer f.Close()

	var list []Pattern
	if err := json.NewDecoder(f).Decode(&list); err != nil {
		return nil, fmt.Errorf("patterns: decode %s: %w", path, err)
	}
	for _, p := range list {
		if p.ID == "" {
			continue
		}
		s.data[p.ID] = p
	}
	return s, nil
}

// Upsert inserts or replaces a pattern.  Returns an error if the pattern is
// missing an id or centroid; centroid length is preserved as-is (the matcher
// requires it to match the embedder's dimensionality).
//
// specialist_url is OPTIONAL: under multi-LoRA serving the proxy routes by
// adapter name (the pattern id) to a single warm --serve-url, so per-pattern
// URLs are a legacy fallback used only when --serve-url is unset.
func (s *Store) Upsert(p Pattern) error {
	if p.ID == "" {
		return errors.New("patterns: id is required")
	}
	if len(p.Centroid) == 0 {
		return errors.New("patterns: centroid is required")
	}
	s.mu.Lock()
	s.data[p.ID] = p
	snapshot := make([]Pattern, 0, len(s.data))
	for _, v := range s.data {
		snapshot = append(snapshot, v)
	}
	s.mu.Unlock()
	return s.persist(snapshot)
}

// List returns a snapshot of all patterns.  Order is not stable.
func (s *Store) List() []Pattern {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Pattern, 0, len(s.data))
	for _, p := range s.data {
		out = append(out, p)
	}
	return out
}

// BestMatch returns the highest-similarity pattern at or above threshold.
// (ok=false, _) when no pattern is registered or all similarities are below
// threshold.  Embeddings whose length doesn't match a pattern's centroid are
// skipped for that pattern.
func (s *Store) BestMatch(embedding []float32, threshold float32) (Match, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best Match
	found := false
	for _, p := range s.data {
		if len(p.Centroid) != len(embedding) {
			continue
		}
		sim := cosine(embedding, p.Centroid)
		if sim < threshold {
			continue
		}
		if !found || sim > best.Similarity {
			best = Match{Pattern: p, Similarity: sim}
			found = true
		}
	}
	return best, found
}

func (s *Store) persist(snapshot []Pattern) error {
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".patterns-*.json")
	if err != nil {
		return fmt.Errorf("patterns: create temp: %w", err)
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snapshot); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("patterns: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("patterns: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("patterns: rename %s: %w", s.path, err)
	}
	return nil
}

// cosine assumes a and b are L2-normalized (as BGE-small embeddings are by
// our embedder), so this is just the dot product.
func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	if math.IsNaN(dot) {
		return 0
	}
	return float32(dot)
}
