package main_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eschmechel/hermes-apprentice/detector/internal/clusterer"
	"github.com/eschmechel/hermes-apprentice/detector/internal/dedupstore"
	"github.com/eschmechel/hermes-apprentice/detector/internal/embedder"
	"github.com/eschmechel/hermes-apprentice/detector/internal/hasher"
	"github.com/eschmechel/hermes-apprentice/detector/internal/httpapi"
	"github.com/eschmechel/hermes-apprentice/detector/internal/patternstore"

	ort "github.com/yalue/onnxruntime_go"
)

func init() {
	ort.SetSharedLibraryPath("/usr/lib/libonnxruntime.so")
}

// TestPipelineEndToEnd exercises the full Apprentice pipeline with 25 similar
// email-extraction inputs: hash → dedup → embed → cluster → save pattern →
// serve via HTTP.  Satisfies the detector exit criterion:
// "With 25 seeded email-extraction inputs, detector emits one candidate
// pattern above threshold."
func TestPipelineEndToEnd(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping ONNX integration test in CI")
	}
	modelDir := filepath.Join(os.Getenv("HOME"), ".apprentice/models/bge-small-onnx")
	if _, err := os.Stat(filepath.Join(modelDir, "model.onnx")); os.IsNotExist(err) {
		t.Skipf("ONNX model not found at %s", modelDir)
	}

	// ---- 1. 25 seeded email-extraction inputs ----
	inputs := []string{
		"extract the sender name from this email",
		"pull out the email address from this message",
		"get the subject line from the email below",
		"find the recipient name in this email thread",
		"extract the date from this email header",
		"pull the sender email from the raw message",
		"extract all email addresses from the body",
		"get the cc recipients from this email",
		"find the bcc field in the email headers",
		"extract the reply to address from this email",
		"pull the message id from this email header",
		"get the in reply to field from this email",
		"extract the user agent from the email headers",
		"find the content type from this email",
		"pull the x mailer header from this message",
		"get the received from header in this email",
		"extract the arc authentication results from headers",
		"find the dkim signature in this email",
		"pull the spf result from this email header",
		"get the dmarc status from the email headers",
		"extract the list unsubscribe header from this email",
		"find the return path address in this email",
		"pull the envelope from address from this message",
		"get the delivered to address from this email",
		"extract the x original to header from this email",
	}

	t.Logf("pipeline: %d seeded inputs", len(inputs))

	// ---- 2. Hash + dedup ----
	storeDir := t.TempDir()
	ds, err := dedupstore.Open(
		filepath.Join(storeDir, "dedup.db"),
		24*time.Hour,
		nil,
	)
	if err != nil {
		t.Fatalf("open dedup store: %v", err)
	}
	defer ds.Close()

	ctx := context.Background()
	var uniqueInputs []string
	for _, in := range inputs {
		h := hasher.Hash(in)
		skip, err := ds.SkipOrMark(ctx, h)
		if err != nil {
			t.Fatalf("SkipOrMark: %v", err)
		}
		if !skip {
			uniqueInputs = append(uniqueInputs, in)
		}
	}
	t.Logf("dedup: %d/%d inputs unique", len(uniqueInputs), len(inputs))

	// ---- 3. Embed ----
	if err := ort.InitializeEnvironment(); err != nil {
		t.Fatalf("Init ONNX env: %v", err)
	}
	defer ort.DestroyEnvironment()

	e, err := embedder.New(
		filepath.Join(modelDir, "model.onnx"),
		filepath.Join(modelDir, "vocab.json"),
	)
	if err != nil {
		t.Fatalf("New embedder: %v", err)
	}
	defer e.Close()

	embeddings := make([][]float32, 0, len(uniqueInputs))
	for _, in := range uniqueInputs {
		vec, err := e.Embed(in)
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		embeddings = append(embeddings, vec)
	}
	t.Logf("embeddings: %d vectors computed", len(embeddings))

	// ---- 4. Cluster ----
	clusters := clusterer.Find(embeddings, clusterer.Config{
		CosineThreshold: 0.78,
		MinClusterSize:  20,
		MinSamples:      1,
	})
	t.Logf("clusters: %d found", len(clusters))

	if len(clusters) == 0 {
		// Dump per-input self-similarity for debugging.
		t.Log("=== DEBUG: no clusters found ===")
		for i := 1; i < len(embeddings); i++ {
			dot := cosSim(embeddings[0], embeddings[i])
			t.Logf("  cos(0, %d) = %.4f → %q", i, dot, uniqueInputs[i][:40])
		}
		t.Fatal("expected at least 1 cluster of ≥20 inputs; got 0")
	}
	if len(clusters) != 1 {
		t.Fatalf("expected exactly 1 cluster, got %d", len(clusters))
	}
	c := clusters[0]
	t.Logf("cluster: size=%d cosine_threshold=0.78", c.Size)
	if c.Size < 20 {
		t.Fatalf("cluster size = %d, want ≥ 20", c.Size)
	}

	// ---- 5. Save pattern ----
	ps, err := patternstore.Open(filepath.Join(storeDir, "patterns"))
	if err != nil {
		t.Fatalf("open pattern store: %v", err)
	}

	pid, err := ps.Save(patternstore.Manifest{
		Description: "Extract structured fields from email headers and body",
		Centroid:    c.Centroid,
		RecordCount: c.Size,
		Status:      patternstore.StatusCandidate,
	})
	if err != nil {
		t.Fatalf("save pattern: %v", err)
	}
	t.Logf("pattern saved: %s", pid)

	// ---- 6. Verify via HTTP API ----
	srv := httpapi.New(httpapi.Config{
		Addr:         ":0",
		PatternStore: ps,
	})
	req := httptest.NewRequest("GET", "/patterns", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("GET /patterns status = %d", rec.Code)
	}
	var patterns []patternstore.Manifest
	if err := json.NewDecoder(rec.Body).Decode(&patterns); err != nil {
		t.Fatalf("decode /patterns: %v", err)
	}
	if len(patterns) != 1 {
		t.Fatalf("GET /patterns returned %d patterns, want 1", len(patterns))
	}
	p := patterns[0]
	if p.ID != pid {
		t.Fatalf("pattern id = %s, want %s", p.ID, pid)
	}
	if p.Status != patternstore.StatusCandidate {
		t.Fatalf("pattern status = %s, want %s", p.Status, patternstore.StatusCandidate)
	}
	if p.RecordCount < 20 {
		t.Fatalf("record count = %d, want ≥ 20", p.RecordCount)
	}
	if p.RecordCount != c.Size {
		t.Fatalf("record count = %d, want cluster size %d", p.RecordCount, c.Size)
	}
	t.Logf("HTTP GET /patterns: 1 pattern returned, record_count=%d, status=%s", p.RecordCount, p.Status)
}

func cosSim(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
