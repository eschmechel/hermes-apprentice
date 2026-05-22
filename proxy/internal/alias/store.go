package alias

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	AliasID   string `json:"alias_id"`
	TargetID  string `json:"target_id"`
	CreatedAt string `json:"created_at"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data map[string]string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("alias: mkdir %s: %w", filepath.Dir(path), err)
	}
	s := &Store{path: path, data: make(map[string]string)}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("alias: open %s: %w", path, err)
	}
	defer f.Close()
	var list []Entry
	if err := json.NewDecoder(f).Decode(&list); err != nil {
		return nil, fmt.Errorf("alias: decode %s: %w", path, err)
	}
	for _, e := range list {
		s.data[e.AliasID] = e.TargetID
	}
	return s, nil
}

func (s *Store) Resolve(aliasID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.data[aliasID]
	return target, ok
}

func (s *Store) Register(aliasID, targetID string) error {
	if aliasID == "" || targetID == "" {
		return errors.New("alias: alias_id and target_id are required")
	}
	if aliasID == targetID {
		return errors.New("alias: alias_id cannot equal target_id")
	}
	s.mu.Lock()
	s.data[aliasID] = targetID
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) Remove(aliasID string) error {
	s.mu.Lock()
	delete(s.data, aliasID)
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.data))
	for aliasID, targetID := range s.data {
		out = append(out, Entry{
			AliasID:   aliasID,
			TargetID:  targetID,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (s *Store) persist() error {
	s.mu.RLock()
	list := make([]Entry, 0, len(s.data))
	for aliasID, targetID := range s.data {
		list = append(list, Entry{AliasID: aliasID, TargetID: targetID})
	}
	s.mu.RUnlock()
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".aliases-*.json")
	if err != nil {
		return fmt.Errorf("alias: create temp: %w", err)
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(list); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("alias: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("alias: close temp: %w", err)
	}
	return os.Rename(tmpName, s.path)
}
