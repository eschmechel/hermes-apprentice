package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestHealthz(t *testing.T) {
	srv := New(Config{
		Addr:        ":0",
		Logger:      nil,
		RegistryDir: t.TempDir(),
	})
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("want status=ok, got %s", body["status"])
	}
}

func TestLatestNotFound(t *testing.T) {
	srv := New(Config{
		Addr:        ":0",
		Logger:      nil,
		RegistryDir: t.TempDir(),
	})
	req := httptest.NewRequest("GET", "/registry/nonexistent/latest", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp latestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Found {
		t.Fatal("expected found=false for nonexistent skill")
	}
	if resp.SkillID != "nonexistent" {
		t.Fatalf("want skill_id=nonexistent, got %s", resp.SkillID)
	}
}

func TestLatestFound(t *testing.T) {
	regDir := t.TempDir()
	skillDir := filepath.Join(regDir, "demo-skill")
	v1Dir := filepath.Join(skillDir, "v1")
	os.MkdirAll(v1Dir, 0o755)

	manifest := map[string]interface{}{
		"schema_version": 1,
		"pattern_id":     "demo-skill",
		"version":        1,
		"promoted_at":    "2026-05-19T00:00:00Z",
		"scores": map[string]float64{
			"exact_match": 0.85,
			"f1":          0.90,
		},
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(v1Dir, "registry_manifest.json"), data, 0o644)

	srv := New(Config{
		Addr:        ":0",
		Logger:      nil,
		RegistryDir: regDir,
	})
	req := httptest.NewRequest("GET", "/registry/demo-skill/latest", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp latestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Found {
		t.Fatal("expected found=true")
	}
	if resp.SkillID != "demo-skill" {
		t.Fatalf("want skill_id=demo-skill, got %s", resp.SkillID)
	}
	if resp.Version != 1 {
		t.Fatalf("want version=1, got %d", resp.Version)
	}

	var parsedManifest map[string]interface{}
	json.Unmarshal(resp.Manifest, &parsedManifest)
	if parsedManifest["pattern_id"] != "demo-skill" {
		t.Fatalf("manifest pattern_id mismatch")
	}
}

func TestLatestPicksHighestVersion(t *testing.T) {
	regDir := t.TempDir()
	skillDir := filepath.Join(regDir, "test-skill")
	for v := 1; v <= 5; v++ {
		vDir := filepath.Join(skillDir, "v"+strconv.Itoa(v))
		os.MkdirAll(vDir, 0o755)
		manifest := fmt.Sprintf(`{"pattern_id":"test-skill","version":%d}`, v)
		os.WriteFile(filepath.Join(vDir, "registry_manifest.json"), []byte(manifest), 0o644)
	}

	srv := New(Config{
		Addr:        ":0",
		Logger:      nil,
		RegistryDir: regDir,
	})
	req := httptest.NewRequest("GET", "/registry/test-skill/latest", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	var resp latestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Version != 5 {
		t.Fatalf("want version=5, got %d\nresponse: %s", resp.Version, w.Body.String())
	}
}

func TestLatestSkipsNonVersionDirs(t *testing.T) {
	regDir := t.TempDir()
	skillDir := filepath.Join(regDir, "test-skill")
	for _, name := range []string{"v1", "v2", "not-a-version", "vx", "v10"} {
		d := filepath.Join(skillDir, name)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "registry_manifest.json"), []byte(`{}`), 0o644)
	}

	srv := New(Config{
		Addr:        ":0",
		Logger:      nil,
		RegistryDir: regDir,
	})
	req := httptest.NewRequest("GET", "/registry/test-skill/latest", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	var resp latestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Version != 10 {
		t.Fatalf("want version=10 (v10 is highest valid), got %d", resp.Version)
	}
}

func TestFindLatestEmpty(t *testing.T) {
	v, err := findLatest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("want 0 for empty dir, got %d", v)
	}
}
