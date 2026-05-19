// Package state persists the observer's high-water mark (the largest
// messages.id we've successfully processed) so a restart resumes from there
// instead of either re-replaying everything or skipping messages produced
// while the observer was down.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

const stateFilename = "observer.state.json"

type record struct {
	LastProcessedID int64 `json:"last_processed_id"`
}

// HWM is a thread-safe, file-backed counter. Writes are atomic via the
// classic tempfile+rename dance so a crash mid-write never produces a
// partially-truncated state file.
type HWM struct {
	path string
	mu   sync.Mutex
	cur  int64
}

// Load reads the existing high-water mark from <dir>/observer.state.json, or
// returns a fresh HWM at 0 if no state file exists yet. The directory is
// created if missing.
func Load(dir string) (*HWM, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir state dir: %w", err)
	}
	path := filepath.Join(dir, stateFilename)
	h := &HWM{path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	h.cur = r.LastProcessedID
	return h, nil
}

// Get returns the current high-water mark.
func (h *HWM) Get() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur
}

// Set atomically writes a new high-water mark. Lower values are ignored so
// out-of-order callers can't accidentally rewind the cursor.
func (h *HWM) Set(id int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id <= h.cur {
		return nil
	}
	data, err := json.Marshal(record{LastProcessedID: id})
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".observer.state.*.tmp")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tempfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), h.path); err != nil {
		return fmt.Errorf("rename to %s: %w", h.path, err)
	}
	h.cur = id
	return nil
}
