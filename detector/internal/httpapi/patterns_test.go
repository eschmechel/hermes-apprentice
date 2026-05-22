package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eschmechel/hermes-apprentice/detector/internal/patternstore"
)

func TestGetPatterns_Empty(t *testing.T) {
	ps, err := patternstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open pattern store: %v", err)
	}
	mux := http.NewServeMux()
	NewPatternHandler(ps).Register(mux)

	req := httptest.NewRequest("GET", "/patterns", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("GET /patterns status = %d, want 200", rec.Code)
	}
	var out []patternstore.Manifest
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %d items", len(out))
	}
}

func TestGetPatterns_WithData(t *testing.T) {
	ps, err := patternstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open pattern store: %v", err)
	}
	id, err := ps.Save(patternstore.Manifest{
		Description: "Extract fields from emails",
		RecordCount: 25,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	mux := http.NewServeMux()
	NewPatternHandler(ps).Register(mux)

	req := httptest.NewRequest("GET", "/patterns", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []patternstore.Manifest
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(out))
	}
	if out[0].ID != id {
		t.Fatalf("id = %s, want %s", out[0].ID, id)
	}
}

func TestApprovePattern(t *testing.T) {
	ps, err := patternstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open pattern store: %v", err)
	}
	id, err := ps.Save(patternstore.Manifest{
		Description: "Summarize articles",
		RecordCount: 30,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	mux := http.NewServeMux()
	NewPatternHandler(ps).Register(mux)

	req := httptest.NewRequest("POST", "/patterns/"+id+"/approve", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("POST /patterns/%s/approve status = %d, want 200", id, rec.Code)
	}
	var m patternstore.Manifest
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Status != patternstore.StatusApproved {
		t.Fatalf("status = %s, want %s", m.Status, patternstore.StatusApproved)
	}

	// Verify persistence
	loaded, err := ps.Load(id)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if loaded.Status != patternstore.StatusApproved {
		t.Fatalf("persisted status = %s, want %s", loaded.Status, patternstore.StatusApproved)
	}
}

func TestApprovePattern_NotFound(t *testing.T) {
	ps, err := patternstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open pattern store: %v", err)
	}
	mux := http.NewServeMux()
	NewPatternHandler(ps).Register(mux)

	req := httptest.NewRequest("POST", "/patterns/00000000-0000-0000-0000-000000000000/approve", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	srv := New(Config{Addr: ":0"})
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["status"] != "ok" {
		t.Fatalf("status = %q, want ok", out["status"])
	}
}

func TestNilStoreReturnsEmptyList(t *testing.T) {
	mux := http.NewServeMux()
	NewPatternHandler(nil).Register(mux)

	req := httptest.NewRequest("GET", "/patterns", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []patternstore.Manifest
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %d items", len(out))
	}
}
