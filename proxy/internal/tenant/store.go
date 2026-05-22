// Package tenant manages multi-tenant authentication and per-tenant state.
//
// Tenants are identified by an X-Apprentice-Tenant header.  Each tenant has an
// API key stored at ~/.apprentice/tenants/{tenant}/.apikey.  The special tenant
// "global" holds patterns and API keys shared across all tenants.
package tenant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const GlobalTenant = "global"

type Config struct {
	TenantRoot string // ~/.apprentice/tenants
	GlobalKey  string // Admin API key for global patterns
}

type Tenant struct {
	ID     string `json:"id"`
	APIKey string `json:"-"`
}

type Store struct {
	mu        sync.RWMutex
	root      string
	globalKey string
	tenants   map[string]*Tenant
}

func Open(cfg Config) (*Store, error) {
	if err := os.MkdirAll(cfg.TenantRoot, 0o755); err != nil {
		return nil, fmt.Errorf("tenant: mkdir %s: %w", cfg.TenantRoot, err)
	}
	s := &Store{
		root:      cfg.TenantRoot,
		globalKey: cfg.GlobalKey,
		tenants:   make(map[string]*Tenant),
	}
	// Scan existing tenant dirs.
	entries, err := os.ReadDir(cfg.TenantRoot)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				id := e.Name()
				key := s.readKey(id)
				if key != "" {
					s.tenants[id] = &Tenant{ID: id, APIKey: key}
				}
			}
		}
	}
	// Always register global tenant if a global key was provided.
	if cfg.GlobalKey != "" {
		s.tenants[GlobalTenant] = &Tenant{ID: GlobalTenant, APIKey: cfg.GlobalKey}
	}
	return s, nil
}

func (s *Store) readKey(tenantID string) string {
	data, err := os.ReadFile(filepath.Join(s.root, tenantID, ".apikey"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// RegisterTenant creates or updates a tenant directory and stores its API key.
func (s *Store) RegisterTenant(id, apiKey string) error {
	if id == "" || id == GlobalTenant {
		return fmt.Errorf("tenant: invalid id %q", id)
	}
	dir := filepath.Join(s.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tenant: mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".apikey"), []byte(strings.TrimSpace(apiKey)+"\n"), 0o600); err != nil {
		return fmt.Errorf("tenant: write key: %w", err)
	}
	s.mu.Lock()
	s.tenants[id] = &Tenant{ID: id, APIKey: apiKey}
	s.mu.Unlock()
	return nil
}

// Authenticate checks if tenantID and apiKey match.  Returns the resolved
// tenant ID (which may differ if an alias was used).  ok=false means denied.
func (s *Store) Authenticate(tenantID, apiKey string) (resolved string, ok bool) {
	if tenantID == "" {
		tenantID = GlobalTenant
	}
	s.mu.RLock()
	t, exists := s.tenants[tenantID]
	s.mu.RUnlock()
	if exists && t.APIKey == apiKey {
		return tenantID, true
	}
	return "", false
}

// ListTenants returns all registered tenant IDs.
func (s *Store) ListTenants() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.tenants))
	for id := range s.tenants {
		out = append(out, id)
	}
	return out
}

func (s *Store) SaveState() error {
	info := make([]Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		if t.ID != GlobalTenant {
			info = append(info, *t)
		}
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.root, "tenants.json"), out, 0o644)
}
