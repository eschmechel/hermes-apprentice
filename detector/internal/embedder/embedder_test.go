package embedder

import (
	"os"
	"path/filepath"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

func init() {
	ort.SetSharedLibraryPath("/usr/lib/libonnxruntime.so")
}

func TestLoadTokenizer(t *testing.T) {
	tok, err := LoadTokenizer(filepath.Join(os.Getenv("HOME"), ".apprentice/models/bge-small-onnx/vocab.json"))
	if err != nil {
		t.Fatalf("LoadTokenizer: %v", err)
	}
	if tok.clsID == 0 {
		t.Fatal("clsID is 0 — vocab likely missing [CLS]")
	}
	if tok.sepID == 0 {
		t.Fatal("sepID is 0 — vocab likely missing [SEP]")
	}
}

func TestTokenizerEncode(t *testing.T) {
	tok, err := LoadTokenizer(filepath.Join(os.Getenv("HOME"), ".apprentice/models/bge-small-onnx/vocab.json"))
	if err != nil {
		t.Fatalf("LoadTokenizer: %v", err)
	}

	ids, mask, typeIDs := tok.Encode("extract name from this email")
	if len(ids) != maxSeqLen {
		t.Fatalf("len(ids) = %d, want %d", len(ids), maxSeqLen)
	}
	if len(mask) != maxSeqLen {
		t.Fatalf("len(mask) = %d, want %d", len(mask), maxSeqLen)
	}
	if len(typeIDs) != maxSeqLen {
		t.Fatalf("len(typeIDs) = %d, want %d", len(typeIDs), maxSeqLen)
	}
	// [CLS] token should be at position 0
	if ids[0] != int64(tok.clsID) {
		t.Fatalf("ids[0] = %d, want clsID %d", ids[0], tok.clsID)
	}
	if mask[0] != 1 {
		t.Fatalf("mask[0] = %d, want 1", mask[0])
	}
	// [SEP] should have mask=1, trailing pads should have mask=0
	foundSEP := false
	padCount := 0
	for i := 1; i < maxSeqLen && !foundSEP; i++ {
		if ids[i] == int64(tok.padID) && mask[i] == 0 {
			padCount++
		} else {
			if padCount > 0 {
				t.Fatalf("pad token at position %d has non-zero tokens after it at position %d", i-padCount, i)
			}
		}
		if ids[i] == int64(tok.sepID) {
			foundSEP = true
		}
	}
	if !foundSEP {
		t.Fatal("no [SEP] token found in encoded sequence")
	}
}

func TestEmbedderBasics(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping ONNX test in CI (needs libonnxruntime.so)")
	}

	modelDir := filepath.Join(os.Getenv("HOME"), ".apprentice/models/bge-small-onnx")
	if _, err := os.Stat(filepath.Join(modelDir, "model.onnx")); os.IsNotExist(err) {
		t.Skipf("ONNX model not found at %s — export first", modelDir)
	}

	if err := ort.InitializeEnvironment(); err != nil {
		t.Fatalf("Init ONNX env: %v", err)
	}
	defer ort.DestroyEnvironment()

	e, err := New(
		filepath.Join(modelDir, "model.onnx"),
		filepath.Join(modelDir, "vocab.json"),
	)
	if err != nil {
		t.Fatalf("New embedder: %v", err)
	}
	defer e.Close()

	vec, err := e.Embed("extract name from this email")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != embeddingDim {
		t.Fatalf("embedding dim = %d, want %d", len(vec), embeddingDim)
	}

	// L2 norm should be 1.0
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq < 0.99 || sumSq > 1.01 {
		t.Fatalf("L2 norm^2 = %.6f, want ~1.0", sumSq)
	}
}

func TestEmbedDifferentInput(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping ONNX test in CI")
	}

	modelDir := filepath.Join(os.Getenv("HOME"), ".apprentice/models/bge-small-onnx")
	if _, err := os.Stat(filepath.Join(modelDir, "model.onnx")); os.IsNotExist(err) {
		t.Skipf("ONNX model not found at %s", modelDir)
	}

	if err := ort.InitializeEnvironment(); err != nil {
		t.Fatalf("Init ONNX env: %v", err)
	}
	defer ort.DestroyEnvironment()

	e, err := New(
		filepath.Join(modelDir, "model.onnx"),
		filepath.Join(modelDir, "vocab.json"),
	)
	if err != nil {
		t.Fatalf("New embedder: %v", err)
	}
	defer e.Close()

	vec1, err := e.Embed("extract name from this email")
	if err != nil {
		t.Fatalf("Embed 1: %v", err)
	}
	vec2, err := e.Embed("summarize this article")
	if err != nil {
		t.Fatalf("Embed 2: %v", err)
	}

	// Cosine similarity between two different sentences should be < 0.95
	var dot float64
	for i := range vec1 {
		dot += float64(vec1[i]) * float64(vec2[i])
	}
	if dot > 0.95 {
		t.Fatalf("cosine similarity between different inputs = %.4f, want < 0.95", dot)
	}
}

func TestEmbedIdenticalInput(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping ONNX test in CI")
	}

	modelDir := filepath.Join(os.Getenv("HOME"), ".apprentice/models/bge-small-onnx")
	if _, err := os.Stat(filepath.Join(modelDir, "model.onnx")); os.IsNotExist(err) {
		t.Skipf("ONNX model not found at %s", modelDir)
	}

	if err := ort.InitializeEnvironment(); err != nil {
		t.Fatalf("Init ONNX env: %v", err)
	}
	defer ort.DestroyEnvironment()

	e, err := New(
		filepath.Join(modelDir, "model.onnx"),
		filepath.Join(modelDir, "vocab.json"),
	)
	if err != nil {
		t.Fatalf("New embedder: %v", err)
	}
	defer e.Close()

	v1, _ := e.Embed("extract name from this email")
	v2, _ := e.Embed("extract name from this email")

	var dot float64
	for i := range v1 {
		dot += float64(v1[i]) * float64(v2[i])
	}
	if dot < 0.999 {
		t.Fatalf("cosine similarity of identical inputs = %.6f, want > 0.999", dot)
	}
}
