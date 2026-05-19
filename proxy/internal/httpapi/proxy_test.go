package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hermes-apprentice/proxy/internal/patterns"
)

// fakeEmbedder returns a configurable vector for each call.  Lets us drive
// the cosine match deterministically without ONNX.
type fakeEmbedder struct {
	byText map[string][]float32
	def    []float32
	err    error
}

func (f *fakeEmbedder) Embed(text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.byText[text]; ok {
		return v, nil
	}
	return f.def, nil
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(sum))
	if n == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

func openAIChatRequest(t *testing.T, userMsg string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "test-model",
		"messages": []map[string]string{
			{"role": "system", "content": "you are helpful"},
			{"role": "user", "content": userMsg},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func openAIChatResponse(content string) string {
	resp, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "test-model",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]string{
					"role":    "assistant",
					"content": content,
				},
			},
		},
	})
	return string(resp)
}

func newTestServer(t *testing.T, cfg Config) (*httptest.Server, *Server) {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if cfg.MatchThreshold == 0 {
		cfg.MatchThreshold = 0.78
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	s := New(cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func openStore(t *testing.T) *patterns.Store {
	t.Helper()
	s, err := patterns.Open(filepath.Join(t.TempDir(), "patterns.json"))
	if err != nil {
		t.Fatalf("patterns.Open: %v", err)
	}
	return s
}

// 01 + 02: OpenAI-compatible endpoint forwards to upstream when no embedder.
func TestProxy_PassThroughToUpstream(t *testing.T) {
	var upstreamHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamHits, 1)
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: openStore(t),
		// no embedder, no patterns → always pass through
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "hello")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "from upstream") {
		t.Fatalf("expected upstream content, got %s", string(body))
	}
	if atomic.LoadInt32(&upstreamHits) != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", upstreamHits)
	}
}

// 03 + 04: matched request routes to specialist instead of upstream.
func TestProxy_MatchedRequestRoutesToSpecialist(t *testing.T) {
	var specHits, upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	specialist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&specHits, 1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected specialist path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from specialist"))
	}))
	defer specialist.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	if err := store.Upsert(patterns.Pattern{
		ID: "p1", Description: "test", Centroid: centroid, SpecialistURL: specialist.URL,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	emb := &fakeEmbedder{byText: map[string][]float32{
		"matchy text": centroid,
	}}

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     emb,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "matchy text")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "from specialist") {
		t.Fatalf("expected specialist content, got %s", string(body))
	}
	if atomic.LoadInt32(&specHits) != 1 {
		t.Fatalf("expected 1 specialist hit, got %d", specHits)
	}
	if atomic.LoadInt32(&upHits) != 0 {
		t.Fatalf("expected no upstream hits, got %d", upHits)
	}
}

// 03 (negative): when embedding is far from centroid, request goes upstream.
func TestProxy_NoMatchGoesUpstream(t *testing.T) {
	var specHits, upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	specialist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&specHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("nope"))
	}))
	defer specialist.Close()

	store := openStore(t)
	_ = store.Upsert(patterns.Pattern{
		ID: "p1", Centroid: normalize([]float32{1, 0, 0, 0}), SpecialistURL: specialist.URL,
	})

	emb := &fakeEmbedder{def: normalize([]float32{0, 1, 0, 0})} // orthogonal → cos = 0

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     emb,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "irrelevant")))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer resp.Body.Close()

	if atomic.LoadInt32(&specHits) != 0 {
		t.Fatalf("expected no specialist hits, got %d", specHits)
	}
	if atomic.LoadInt32(&upHits) != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", upHits)
	}
}

// 05: specialist non-200 triggers fallback to upstream.
func TestProxy_FallbackOnSpecialist500(t *testing.T) {
	var specHits, upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("rescued"))
	}))
	defer upstream.Close()

	specialist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&specHits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer specialist.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	_ = store.Upsert(patterns.Pattern{
		ID: "p1", Centroid: centroid, SpecialistURL: specialist.URL,
	})

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     &fakeEmbedder{def: centroid},
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "anything")))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "rescued") {
		t.Fatalf("expected upstream fallback, got %s", string(body))
	}
	if atomic.LoadInt32(&specHits) != 1 || atomic.LoadInt32(&upHits) != 1 {
		t.Fatalf("spec=%d up=%d", specHits, upHits)
	}
}

// 05: specialist returns 200 but without "choices" — counts as failure.
func TestProxy_FallbackOnMissingChoices(t *testing.T) {
	var upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("rescued"))
	}))
	defer upstream.Close()

	specialist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"error":{"message":"model loading"}}`)
	}))
	defer specialist.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	_ = store.Upsert(patterns.Pattern{
		ID: "p1", Centroid: centroid, SpecialistURL: specialist.URL,
	})

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     &fakeEmbedder{def: centroid},
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "anything")))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "rescued") {
		t.Fatalf("expected upstream rescue, got %s", string(body))
	}
	if atomic.LoadInt32(&upHits) != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", upHits)
	}
}

// 06: shadow sampling — when rate=1.0 every matched call also hits upstream.
func TestProxy_ShadowSamplingFiresUpstream(t *testing.T) {
	var specHits, upHits int32
	done := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("shadowed"))
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer upstream.Close()

	specialist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&specHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("specialist"))
	}))
	defer specialist.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	_ = store.Upsert(patterns.Pattern{
		ID: "p1", Centroid: centroid, SpecialistURL: specialist.URL,
	})

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     &fakeEmbedder{def: centroid},
		ShadowRate:   1.0,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "shadow me")))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "specialist") {
		t.Fatalf("expected specialist response to user, got %s", string(body))
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream shadow call never arrived")
	}
	if atomic.LoadInt32(&specHits) != 1 || atomic.LoadInt32(&upHits) != 1 {
		t.Fatalf("spec=%d up=%d", specHits, upHits)
	}
}

// 06: shadow_rate=0 — upstream is NEVER called on a successful match.
func TestProxy_NoShadowWhenRateZero(t *testing.T) {
	var upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	specialist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("specialist"))
	}))
	defer specialist.Close()

	centroid := normalize([]float32{1, 0, 0, 0})
	store := openStore(t)
	_ = store.Upsert(patterns.Pattern{
		ID: "p1", Centroid: centroid, SpecialistURL: specialist.URL,
	})

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     &fakeEmbedder{def: centroid},
		ShadowRate:   0,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "noshadow")))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer resp.Body.Close()

	// Give a moment in case a goroutine sneaks through
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&upHits) != 0 {
		t.Fatalf("expected 0 upstream hits with shadow_rate=0, got %d", upHits)
	}
}

// 07: POST /patterns registers a pattern.
func TestProxy_PostPatternsRegistration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	store, err := patterns.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ts, _ := newTestServer(t, Config{
		UpstreamURL:  "http://unused.invalid",
		PatternStore: store,
	})

	pat := patterns.Pattern{
		ID:            "p-new",
		Description:   "test pattern",
		Centroid:      []float32{0.1, 0.9, 0.0},
		SpecialistURL: "http://localhost:8000",
	}
	body, _ := json.Marshal(pat)
	resp, err := http.Post(ts.URL+"/patterns", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(buf))
	}

	// GET /patterns returns it
	getResp, err := http.Get(ts.URL + "/patterns")
	if err != nil { t.Fatalf("GET: %v", err) }
	defer getResp.Body.Close()
	var list []patterns.Pattern
	if err := json.NewDecoder(getResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "p-new" {
		t.Fatalf("list mismatch: %+v", list)
	}

	// Persisted to disk
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected patterns.json on disk: %v", err)
	}
}

// 07: POST /patterns rejects malformed bodies.
func TestProxy_PostPatternsRejectsBad(t *testing.T) {
	ts, _ := newTestServer(t, Config{
		UpstreamURL:  "http://unused.invalid",
		PatternStore: openStore(t),
	})

	cases := []string{
		`{}`,
		`{"id":"x","centroid":[]}`,
		`not json`,
	}
	for _, body := range cases {
		resp, err := http.Post(ts.URL+"/patterns", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %q: %v", body, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected 4xx for %q, got %d", body, resp.StatusCode)
		}
	}
}

// healthz works
func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t, Config{
		UpstreamURL:  "http://unused.invalid",
		PatternStore: openStore(t),
	})
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// embedder failure → falls back to upstream pass-through (no panic).
func TestProxy_EmbedderErrorPassesThrough(t *testing.T) {
	var upHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upHits, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openAIChatResponse("from upstream"))
	}))
	defer upstream.Close()

	store := openStore(t)
	_ = store.Upsert(patterns.Pattern{
		ID: "p1", Centroid: []float32{1, 0, 0, 0}, SpecialistURL: "http://unreachable.invalid",
	})

	ts, _ := newTestServer(t, Config{
		UpstreamURL:  upstream.URL,
		PatternStore: store,
		Embedder:     &fakeEmbedder{err: errors.New("simulated embed failure")},
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		bytes.NewReader(openAIChatRequest(t, "hi")))
	if err != nil { t.Fatalf("POST: %v", err) }
	defer resp.Body.Close()
	if atomic.LoadInt32(&upHits) != 1 {
		t.Fatalf("expected upstream fallback, got upHits=%d", upHits)
	}
}
