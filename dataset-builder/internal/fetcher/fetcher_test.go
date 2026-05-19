package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestFetchAll_Empty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pattern") != "p1" {
			t.Errorf("unexpected pattern: %s", r.URL.Query().Get("pattern"))
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	records, err := c.FetchAll(context.Background(), "p1")
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(records))
	}
}

func TestFetchAll_SingleBatch(t *testing.T) {
	records := []Record{
		{ID: 1, InputText: "extract fields", OutputText: "done", CreatedAt: 1716150000},
		{ID: 2, InputText: "parse JSON", OutputText: "ok", CreatedAt: 1716150001},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(records)
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.FetchAll(context.Background(), "p1")
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
}

func TestFetchAll_Pagination(t *testing.T) {
	c := NewClient("http://dummy")

	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqNum := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if reqNum == 1 {
			if r.URL.Query().Get("since") != "" {
				t.Errorf("first request should have no since param, got %s", r.URL.Query().Get("since"))
			}
			_ = json.NewEncoder(w).Encode([]Record{
				{ID: 1, CreatedAt: 100},
				{ID: 2, CreatedAt: 200},
			})
			return
		}
		if r.URL.Query().Get("since") != "200" {
			t.Errorf("second request since=%s, want 200", r.URL.Query().Get("since"))
		}
		_ = json.NewEncoder(w).Encode([]Record{
			{ID: 3, CreatedAt: 300},
		})
	}))
	defer ts.Close()

	c.baseURL = ts.URL
	c.batchSize = 2

	got, err := c.FetchAll(context.Background(), "p1")
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records, got %d", len(got))
	}
}

func TestFetchAll_ObserverError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "down"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	_, err := c.FetchAll(context.Background(), "p1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFetchAll_BadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	_, err := c.FetchAll(context.Background(), "p1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
