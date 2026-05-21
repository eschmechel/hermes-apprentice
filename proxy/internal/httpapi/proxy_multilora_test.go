package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hermes-apprentice/proxy/internal/patterns"
)

// W1: with --serve-url + --residency-url set, a matched request ensures the
// adapter is resident, rewrites the request model to the adapter (pattern) id,
// and routes to the single warm server — never the per-pattern specialist_url.
func TestProxy_MultiLoRARoutesByAdapterName(t *testing.T) {
	var ensuredID, serveModel atomic.Value
	ensuredID.Store("")
	serveModel.Store("")

	residency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/residency/ensure" {
			t.Errorf("unexpected residency path %q", r.URL.Path)
		}
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		ensuredID.Store(b["adapter_id"])
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"loaded":%q}`, b["adapter_id"])
	}))
	defer residency.Close()

	var upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	serve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]json.RawMessage
		_ = json.Unmarshal(raw, &b)
		var m string
		_ = json.Unmarshal(b["model"], &m)
		serveModel.Store(m)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from specialist"))
	}))
	defer serve.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	if err := store.Upsert(patterns.Pattern{ID: "p1", Description: "t", Centroid: centroid}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		ServeURL:     serve.URL,
		ResidencyURL: residency.URL,
		Embedder:     &fakeEmbedder{def: centroid},
		PatternStore: store,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "hello")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "from specialist") {
		t.Fatalf("expected specialist content, got %s", body)
	}
	if got := ensuredID.Load().(string); got != "p1" {
		t.Fatalf("ensure adapter_id = %q, want p1", got)
	}
	if got := serveModel.Load().(string); got != "p1" {
		t.Fatalf("serve model rewritten to %q, want p1", got)
	}
	if atomic.LoadInt32(&upHits) != 0 {
		t.Fatalf("upstream should not be hit, got %d", upHits)
	}
}

// When the residency ensure fails, the proxy must fall back to upstream and
// never hit the warm server with an unloaded adapter.
func TestProxy_MultiLoRAEnsureFailureFallsBack(t *testing.T) {
	residency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer residency.Close()

	var upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	serve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("serve must not be hit when ensure fails")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from specialist"))
	}))
	defer serve.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	if err := store.Upsert(patterns.Pattern{ID: "p1", Centroid: centroid}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		ServeURL:     serve.URL,
		ResidencyURL: residency.URL,
		Embedder:     &fakeEmbedder{def: centroid},
		PatternStore: store,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "hello")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "from upstream") {
		t.Fatalf("expected upstream fallback, got %s", body)
	}
	if atomic.LoadInt32(&upHits) != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", upHits)
	}
}
