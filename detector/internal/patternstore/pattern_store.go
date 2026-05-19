// Package patternstore persists candidate detector patterns to disk as
// ~/.apprentice/patterns/<id>/manifest.json.  Each pattern includes the
// cluster centroid, LLM-generated description, record count, and status.
// Satisfies detector-05.
package patternstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var idRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validID(id string) bool { return idRE.MatchString(id) }

// Status values for a pattern lifecycle.
const (
	StatusCandidate = "candidate"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
)

// Manifest is the on-disk representation of one detected candidate pattern.
type Manifest struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Centroid    []float32 `json:"centroid"`
	RecordCount int       `json:"record_count"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store manages the pattern directory on the local filesystem.
type Store struct {
	baseDir string
}

// New returns a Store rooted at baseDir.  The caller is responsible for
// ensuring baseDir exists (Open will create it if needed).  Typically
// baseDir == filepath.Join(os.Getenv("HOME"), ".apprentice", "patterns").
func New(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// Open ensures baseDir exists and returns a Store.
func Open(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("patternstore: mkdir %s: %w", baseDir, err)
	}
	return New(baseDir), nil
}

// Save writes m to disk.  If m.ID is empty a new UUID is generated.
// Returns the (possibly newly-assigned) ID.
func (s *Store) Save(m Manifest) (string, error) {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.Status == "" {
		m.Status = StatusCandidate
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	dir := filepath.Join(s.baseDir, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("patternstore: mkdir pattern %s: %w", m.ID, err)
	}
	f, err := os.Create(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", fmt.Errorf("patternstore: create manifest for %s: %w", m.ID, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return "", fmt.Errorf("patternstore: encode manifest for %s: %w", m.ID, err)
	}
	return m.ID, nil
}

// Load reads the Manifest for the given pattern ID.
func (s *Store) Load(id string) (Manifest, error) {
	if !validID(id) {
		return Manifest{}, fmt.Errorf("patternstore: invalid id %q", id)
	}
	path := filepath.Join(s.baseDir, id, "manifest.json")
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("patternstore: open %s: %w", id, err)
	}
	defer f.Close()
	var m Manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("patternstore: decode %s: %w", id, err)
	}
	return m, nil
}

// List returns all patterns under baseDir.  Missing or unparseable
// manifests are silently skipped.
func (s *Store) List() ([]Manifest, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Manifest{}, nil
		}
		return nil, fmt.Errorf("patternstore: readdir %s: %w", s.baseDir, err)
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := s.Load(e.Name())
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// SetStatus updates the status field of a pattern's manifest.
func (s *Store) SetStatus(id, status string) error {
	if !validID(id) {
		return fmt.Errorf("patternstore: invalid id %q", id)
	}
	m, err := s.Load(id)
	if err != nil {
		return err
	}
	m.Status = status
	f, err := os.Create(filepath.Join(s.baseDir, id, "manifest.json"))
	if err != nil {
		return fmt.Errorf("patternstore: update manifest for %s: %w", id, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}
