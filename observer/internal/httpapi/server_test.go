package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hermes-apprentice/observer/internal/store"
)

func makeStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func makeServer(t *testing.T, s *store.Store) http.Handler {
	t.Helper()
	srv := New(Config{Addr: ":0", Store: s})
	return srv.srv.Handler
}

func TestRecords_EmptyStoreReturnsEmptyArray(t *testing.T) {
	srv := makeServer(t, makeStore(t))
	req := httptest.NewRequest(http.MethodGet, "/records", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Fatalf("body = %q, want [] (with optional newline)", body)
	}
}

func TestRecords_FilterByPattern(t *testing.T) {
	s := makeStore(t)
	pidA := "patternA"
	pidB := "patternB"
	model := "deepseek-v4-flash"
	for i, pid := range []*string{&pidA, &pidB, &pidA} {
		if _, err := s.InsertRecord(context.Background(), store.Record{
			SessionID:          "sess-" + string(rune('a'+i)),
			PatternID:          pid,
			InputHash:          "h",
			InputText:          "in",
			OutputText:         "out",
			ModelUsed:          &model,
			CreatedAt:          float64(1000 + i),
			UserMessageID:      int64(2 * i),
			AssistantMessageID: int64(2*i + 1),
		}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	srv := makeServer(t, s)
	req := httptest.NewRequest(http.MethodGet, "/records?pattern=patternA", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var got []store.Record
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.PatternID == nil || *r.PatternID != "patternA" {
			t.Fatalf("got pattern %v, want patternA", r.PatternID)
		}
	}
}

func TestRecords_LimitClampsResults(t *testing.T) {
	s := makeStore(t)
	for i := 0; i < 5; i++ {
		_, _ = s.InsertRecord(context.Background(), store.Record{
			SessionID:          "s",
			InputHash:          "h",
			InputText:          "in",
			OutputText:         "out",
			CreatedAt:          float64(1000 + i),
			UserMessageID:      int64(2 * i),
			AssistantMessageID: int64(2*i + 1),
		})
	}

	srv := makeServer(t, s)
	req := httptest.NewRequest(http.MethodGet, "/records?limit=2", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var got []store.Record
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestRecords_InvalidSinceIs400(t *testing.T) {
	srv := makeServer(t, makeStore(t))
	req := httptest.NewRequest(http.MethodGet, "/records?since=notanumber", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
