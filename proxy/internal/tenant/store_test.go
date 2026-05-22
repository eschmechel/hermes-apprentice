package tenant

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesRoot(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{TenantRoot: dir, GlobalKey: ""})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("root should have been created")
	}
	_ = s
}

func TestRegisterAndAuthenticate(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{TenantRoot: dir, GlobalKey: ""})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTenant("acme", "sk-acme-123"); err != nil {
		t.Fatal(err)
	}
	resolved, ok := s.Authenticate("acme", "sk-acme-123")
	if !ok {
		t.Fatal("should authenticate with correct key")
	}
	if resolved != "acme" {
		t.Fatalf("expected resolved='acme', got %q", resolved)
	}
}

func TestAuthenticateWrongKey(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{TenantRoot: dir, GlobalKey: ""})
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterTenant("acme", "sk-acme-123")
	_, ok := s.Authenticate("acme", "wrong-key")
	if ok {
		t.Fatal("should NOT authenticate with wrong key")
	}
}

func TestAuthenticateNonexistentTenant(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(Config{TenantRoot: dir, GlobalKey: ""})
	_, ok := s.Authenticate("nobody", "key")
	if ok {
		t.Fatal("should NOT authenticate nonexistent tenant")
	}
}

func TestGlobalTenantKey(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{TenantRoot: dir, GlobalKey: "global-admin-key"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := s.Authenticate("global", "global-admin-key")
	if !ok {
		t.Fatal("global tenant should authenticate with GlobalKey")
	}
	if resolved != "global" {
		t.Fatalf("expected 'global', got %q", resolved)
	}
}

func TestEmptyTenantDefaultsToGlobal(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(Config{TenantRoot: dir, GlobalKey: "global-key"})
	resolved, ok := s.Authenticate("", "global-key")
	if !ok {
		t.Fatal("empty tenant should authenticate as global")
	}
	if resolved != "global" {
		t.Fatalf("expected 'global', got %q", resolved)
	}
}

func TestListTenants(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(Config{TenantRoot: dir, GlobalKey: "gk"})
	s.RegisterTenant("t1", "k1")
	s.RegisterTenant("t2", "k2")
	list := s.ListTenants()
	m := make(map[string]bool)
	for _, id := range list {
		m[id] = true
	}
	if !m["t1"] {
		t.Fatal("expected t1 in list")
	}
	if !m["t2"] {
		t.Fatal("expected t2 in list")
	}
	if !m["global"] {
		t.Fatal("expected global in list")
	}
}

func TestReadKeyFromDisk(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "acme"), 0o755)
	os.WriteFile(filepath.Join(dir, "acme", ".apikey"), []byte("sk-disk-key\n"), 0o600)

	s, err := Open(Config{TenantRoot: dir, GlobalKey: ""})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := s.Authenticate("acme", "sk-disk-key")
	if !ok {
		t.Fatal("should read key from disk on Open")
	}
	if resolved != "acme" {
		t.Fatalf("expected 'acme', got %q", resolved)
	}
}
